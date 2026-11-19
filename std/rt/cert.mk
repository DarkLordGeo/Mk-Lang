// Why: on Windows, std.x509's Verify checks a chain against the platform's
// trust store when the caller names no pool. That check is a blocking
// crypt32.dll call, so it runs as a std.rt I/O job like any other.
//
// What: CertVerify and CertVerifyCached (std-internal), with their pool jobs
// CertJob and CertCachedJob, and CertMarked.
//
// How: the work runs on a pool thread, never a carrier, since chain building
// may fetch an issuer from the network. crypt32.dll is loaded on the first
// call, so a program that never calls these loads nothing new.

import std.core
import std.raw
import std.sys

/// CertVerify asks the platform to check chain for server authentication
/// at now (Unix nanoseconds) and, when host is not empty, for host. chain
/// is the leaf first, then any intermediates, each a 4-byte little-endian
/// length and its DER, at most sys.CertChainMax. value is the verdict: 0
/// trusted, else the SSL policy's code (sys/windows/cert.mk lists them); os
/// the chain's status bits. Errors: Other for a malformed chain or host, a
/// certificate the platform cannot parse, or crypt32 not loadable (os the
/// code); Unsupported on Linux (std.x509 checks against the system bundle
/// itself); Canceled before the call starts. Pool, whole once started
/// (nothing is held on either side); O(chain) calls; allocates
/// nothing of its own.
export CertVerify(io: Io, chain: []u8, host: str, now: i64): core.IoResult {
    var a := CallOf(core.IoOpCertVerify, core.IoRoutePool, 0, CertJob)
    a.p = raw.Data(chain)
    a.pn = chain.Len()
    a.q = raw.Data(host.AsBytes())
    a.qn = host.Len()
    a.w.a = now
    return Run(io, mut a)
}

CertJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        // A leading empty record is the cache-only mark (CertVerifyCached);
        // here it stays the malformed request it always was: an empty
        // chain, Other on Windows and Unsupported on Linux.
        var chain := In(a)
        if CertMarked(chain) {
            chain = chain[0..0]
        }
        Set(a, sys.CertVerify(chain, In2(a), (*a).w.a))
    }
}

/// CertVerifyCached is CertVerify for tests, with the platform's retrieval
/// from its local cache only and no update of its root list, so the check
/// touches no network (std.x509's tests of the platform path use it).
/// chain is CertVerify's form preceded by an empty record (4 zero bytes);
/// without that mark the request is malformed (Other on Windows). The rest
/// as CertVerify: Unsupported on Linux, Canceled before the call starts;
/// pool, whole once started; O(chain) calls; allocates nothing of
/// its own.
export CertVerifyCached(io: Io, chain: []u8, host: str, now: i64): core.IoResult {
    var a := CallOf(core.IoOpCertVerify, core.IoRoutePool, 0, CertCachedJob)
    a.p = raw.Data(chain)
    a.pn = chain.Len()
    a.q = raw.Data(host.AsBytes())
    a.qn = host.Len()
    a.w.a = now
    return Run(io, mut a)
}

CertCachedJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        var chain := In(a)
        if !CertMarked(chain) {
            chain = chain[0..0]
        }
        Set(a, sys.CertVerify(chain, In2(a), (*a).w.a))
    }
}

// CertMarked is whether chain starts with an empty record. O(1).
CertMarked(chain: []u8): bool {
    return chain.Len() >= 4 && (chain[0] | chain[1] | chain[2] | chain[3]) == 0
}

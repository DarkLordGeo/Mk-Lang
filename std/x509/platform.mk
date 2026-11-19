// Why: on Windows the root store is fetched on demand and the enumerated
// ROOT store can lack valid roots, so trust must be decided by the
// platform's own chain check.
//
// What: Platform, the Windows verdict for the system's pool.
//
// How: the chain goes to CertGetCertificateChain and its SSL policy check at
// the caller's now, through rt.CertVerify on a pool thread, never a
// carrier. Verify has already checked the name and the leaf's key usage,
// so the platform gets no host. More below.

// The SSL policy's verdict maps to std.x509's error, and a passing verdict
// still fails on any trust bit of the chain, as Go's root_windows.go;
// anything the platform could not answer (a target without it, a simulated
// Io, a certificate it cannot parse) fails closed. The platform is asked
// for strong signatures (szOID_CERT_STRONG_SIGN_OS_1,
// std/sys/windows/cert.mk), so a SHA-1, MD5 or MD2 signature on the path it
// builds sets CERT_TRUST_HAS_WEAK_SIGNATURE, which is Unsupported before any
// verdict, as std.x509 refuses those algorithms on its own path.

import std.core
import std.rt
import std.sys
import std.time

// The SSL policy's verdicts (WinError.h) that have an error of their own.
const CertExpired: i64 = 0x800B0101
const CertNameMismatch: i64 = 0x800B010F
const CertWrongUsage: i64 = 0x800B0110
const TrustSignature: i64 = 0x80096004

// The chain's dwErrorStatus bits (wincrypt.h) Go maps to an error of
// their own; every other non-zero status is UnknownAuthority.
const TrustNotTimeValid: i64 = 0x1
const TrustNotValidForUsage: i64 = 0x10
// CERT_TRUST_HAS_WEAK_SIGNATURE: a signature of the path is not strong
// under the policy cert.mk names. The platform also sets
// CERT_TRUST_IS_NOT_SIGNATURE_VALID with it and the SSL policy answers
// TRUST_E_CERT_SIGNATURE (as `go test ./std -run
// 'TestX509_StrongSignBits$' -v` shows), so this bit is read first.
const TrustWeakSignature: i64 = 0x0010_0000

// Platform hands chain to the platform's check at now; cached (tests only)
// limits its retrieval to the local cache, so it touches no network.
Platform(a: &Arena, io: Io, chain: []Certificate, now: time.Time, cached: bool): Error!void {
    if chain.Len() > sys.CertChainMax {
        fail TooComplex
    }
    // rt.CertVerifyCached's mark: an empty record first.
    mark := if cached { 4 } else { 0 }
    var total := mark
    for i in 0..chain.Len() {
        total += 4 + chain[i].data.Len()
    }
    req := try a.Slice[u8](total)
    for i in 0..mark {
        req[i] = 0
    }
    var at := mark
    for i in 0..chain.Len() {
        d := chain[i].data
        k := d.Len()
        req[at] = Trunc[u8](k)
        req[at + 1] = Trunc[u8](k >> 8)
        req[at + 2] = Trunc[u8](k >> 16)
        req[at + 3] = Trunc[u8](k >> 24)
        (mut req[at + 4..at + 4 + k]).CopyFrom(d)
        at += 4 + k
    }
    nanos := now.UnixNanos()
    r := if cached { rt.CertVerifyCached(io, req[..], "", nanos) } else { rt.CertVerify(io, req[..], "", nanos) }
    if r.err == core.SysErr.Canceled {
        fail Canceled
    }
    if r.err != core.SysErr.None {
        fail Unsupported
    }
    if (r.os & TrustWeakSignature) != 0 {
        fail Unsupported
    }
    v := r.value
    if v == 0 {
        // The policy passed: the chain's own trust bits must be clear too,
        // as Go's root_windows.go requires (checkChainTrustStatus), else
        // fail closed. A refused policy's verdict names the error first,
        // so its kind is the one the program's own pool would give.
        t := r.os
        if t == TrustNotTimeValid {
            fail Expired
        }
        if t == TrustNotValidForUsage {
            fail BadUsage
        }
        if t != 0 {
            fail UnknownAuthority
        }
        return
    }
    if v == CertExpired {
        fail Expired
    }
    if v == CertWrongUsage {
        fail BadUsage
    }
    if v == TrustSignature {
        fail BadSignature
    }
    if v == CertNameMismatch {
        fail NameMismatch
    }
    // CERT_E_UNTRUSTEDROOT (0x800B0109), CERT_E_CHAINING (0x800B010A) and
    // every other verdict: no trusted path, failing closed.
    fail UnknownAuthority
}

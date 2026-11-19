// Why: std.x509's Verify with no pool of its own trusts what Windows trusts,
// so it asks the platform's certificate verifier.
//
// What: CertVerify, CertVerifyWith and CertChainMax, over crypt32.dll
// (Crypt32, CryptFn).
//
// How: crypt32.dll is loaded at run time on first use, from System32 only
// (LoadLibraryExW with LOAD_LIBRARY_SEARCH_SYSTEM32), never through the
// import table, so a binary that never verifies loads and imports nothing
// new; the module stays loaded. Called only from a pool thread, since chain
// building may fetch an issuer from the network. As Go's crypto/x509
// (verify_windows.go): the leaf as a context, the rest of the chain in a
// memory store, CertGetCertificateChain for server authentication at the
// caller's time with no revocation check, then the SSL policy
// (CERT_CHAIN_POLICY_SSL) with the host name.

import std.core
import std.raw

/// CertChainMax bounds the certificates of one chain (the leaf and what
/// follows it).
export const CertChainMax: int = 16

const PolicySsl: uint = 4
const AuthServer: u32 = 2
// X509_ASN_ENCODING | PKCS_7_ASN_ENCODING.
const CertEncoding: u32 = 0x0001_0001
const StoreMemory: uint = 2
const StoreDeferClose: u32 = 4
const AddAlways: u32 = 4
const LoadSystem32: u32 = 0x800
// FILETIME of the Unix epoch, in 100 ns units since 1601.
const CertEpoch: i64 = 116_444_736_000_000_000
// HostMax is a DNS name's most bytes (253) and its NUL, rounded up.
const HostMax: int = 256
// CERT_CHAIN_CACHE_ONLY_URL_RETRIEVAL | CERT_CHAIN_DISABLE_AUTH_ROOT_AUTO_UPDATE:
// retrieval from the local cache only, and no root list update, so a test
// of the platform path touches no network.
const ChainCacheOnly: u32 = 0x104
// CERT_STRONG_SIGN_OID_INFO_CHOICE: CERT_STRONG_SIGN_PARA names its
// policy by an OID.
const StrongSignOid: u32 = 2
// CERT_CHAIN_STRONG_SIGN_DISABLE_END_CHECK_FLAG: the end certificate's
// public key is not checked against the strong-sign policy.
const StrongSignNoEndCheck: u32 = 1

// Crypt32 is crypt32.dll's module handle, 0 before the first load.
var Crypt32: raw.Atomic[u64] = raw.Atomic[u64].New(0)

// CryptFn is crypt32's entry named by name (NUL-terminated), loading the
// module on first use; null when the load or the lookup fails.
CryptFn(name: str): *u8 {
    // Edge: this Load and the Store below are sequentially consistent, so a
    // thread that reads a module address reads one LoadLibraryExW returned.
    // Two first uses may both load: the second load is a reference to the
    // same module, and both Stores write its one address.
    var m := Trunc[uint](Crypt32.Load())
    if m == 0 {
        var name16: [16]u16 = [0; 16]
        _ = Wide("crypt32.dll", mut name16[..])
        unsafe "kernel32; name16 is NUL-terminated" {
            m = LoadLibraryExW(raw.Data(name16[..]), 0, LoadSystem32)
        }
        if m == 0 {
            return raw.Null[u8]()
        }
        // Edge: the Store pairs with the Load above (both sequentially
        // consistent): it publishes the module LoadLibraryExW returned.
        Crypt32.Store(u64(m))
    }
    unsafe "kernel32; name is a NUL-terminated constant" {
        return GetProcAddress(m, raw.StrData(name))
    }
}

// CertWord is f's address as a word to read a function value from.
CertWord(f: *u8): u64 {
    return u64(raw.Addr(f))
}

// CertRecords checks chain's records (a 4-byte little-endian length, then
// that many DER bytes, the leaf first) and returns how many there are, or 0
// when one is empty or runs past the end, or there are none or more than
// CertChainMax.
CertRecords(chain: []u8): int {
    var at := 0
    var n := 0
    for at < chain.Len() {
        if chain.Len() - at < 4 || n == CertChainMax {
            return 0
        }
        k := int(chain[at]) | (int(chain[at + 1]) << 8) | (int(chain[at + 2]) << 16) | (int(chain[at + 3]) << 24)
        if k == 0 || k > chain.Len() - at - 4 {
            return 0
        }
        at += 4 + k
        n += 1
    }
    return n
}

/// CertVerify asks the platform to build and check chain (CertRecords'
/// form: the leaf first, then any intermediates, each a 4-byte
/// little-endian length and its DER) for server authentication at now
/// (Unix nanoseconds), with host checked against the leaf when it is not
/// empty. A chain that starts with an empty record (4 zero bytes, which
/// CertRecords refuses) asks for retrieval from the local cache only and no
/// update of the root list; the records after it are the chain
/// (rt.CertVerifyCached, for tests; rt.CertVerify never passes it). value
/// is the SSL policy's verdict, 0 when trusted, else its code
/// (CERT_E_UNTRUSTEDROOT 0x800B0109, CERT_E_EXPIRED 0x800B0101,
/// CERT_E_CN_NO_MATCH 0x800B010F, CERT_E_WRONG_USAGE 0x800B0110,
/// TRUST_E_CERT_SIGNATURE 0x80096004, ...); os is the chain's
/// dwErrorStatus bits. host is a str's bytes (UTF-8). Other: a malformed
/// request (CertRecords 0, a host of 254 bytes or more or holding a NUL), a certificate crypt32
/// cannot parse (os its code, e.g. CRYPT_E_ASN1_BADTAG), crypt32 not
/// loadable, or a call's failure (os GetLastError). Blocking, possibly on
/// the network: a pool thread only. Every context, store and chain it makes
/// is freed on every path. O(chain) calls; allocates nothing of its own
/// (crypt32 allocates and frees its chain).
export CertVerify(chain: []u8, host: []u8, now: i64): OsResult {
    var flags: u32 = 0
    var recs := chain
    if CertCacheMarked(chain) {
        flags = ChainCacheOnly
        recs = chain[4..]
    }
    return CertVerifyWith(recs, host, now, flags)
}

// CertCacheMarked is whether chain starts with an empty record, the mark
// of a cache-only request. O(1).
CertCacheMarked(chain: []u8): bool {
    return chain.Len() >= 4 && (chain[0] | chain[1] | chain[2] | chain[3]) == 0
}

// CertVerifyWith is CertVerify over chain's records with flags for
// CertGetCertificateChain's dwFlags.
CertVerifyWith(chain: []u8, host: []u8, now: i64, flags: u32): OsResult {
    n := CertRecords(chain)
    if n == 0 {
        return FailWith(core.SysErr.Other)
    }
    var host16: [256]u16 = [0; 256]
    if host.Len() > 0 {
        if host.Len() >= HostMax - 2 {
            return FailWith(core.SysErr.Other)
        }
        // host is a str's bytes (rt.CertVerify takes a str), so UTF-8.
        if Wide(PathOf(host), mut host16[..]) < 0 {
            return FailWith(core.SysErr.Other)
        }
    }
    fc := CryptFn("CertCreateCertificateContext\u{0}")
    fs := CryptFn("CertOpenStore\u{0}")
    fa := CryptFn("CertAddEncodedCertificateToStore\u{0}")
    fg := CryptFn("CertGetCertificateChain\u{0}")
    fv := CryptFn("CertVerifyCertificateChainPolicy\u{0}")
    ffc := CryptFn("CertFreeCertificateChain\u{0}")
    ffx := CryptFn("CertFreeCertificateContext\u{0}")
    fcs := CryptFn("CertCloseStore\u{0}")
    first := raw.IsNull(fc) || raw.IsNull(fs) || raw.IsNull(fa) || raw.IsNull(fg)
    rest := raw.IsNull(fv) || raw.IsNull(ffc) || raw.IsNull(ffx) || raw.IsNull(fcs)
    if first || rest {
        return LastError()
    }
    var wc := CertWord(fc)
    var ws := CertWord(fs)
    var wa := CertWord(fa)
    var wg := CertWord(fg)
    var wv := CertWord(fv)
    var wfc := CertWord(ffc)
    var wfx := CertWord(ffx)
    var wcs := CertWord(fcs)
    unsafe "crypt32's entries, each read as its documented signature; chain's records are checked by CertRecords" {
        create := raw.Read(raw.PtrCast[u64, fn(u32, *u8, u32): uint](raw.OfMut(mut wc)))
        open := raw.Read(raw.PtrCast[u64, fn(uint, u32, uint, u32, uint): uint](raw.OfMut(mut ws)))
        add := raw.Read(raw.PtrCast[u64, fn(uint, u32, *u8, u32, u32, uint): i32](raw.OfMut(mut wa)))
        get := raw.Read(raw.PtrCast[u64, fn(uint, uint, *u8, uint, *u8, u32, uint, *uint): i32](raw.OfMut(mut wg)))
        verify := raw.Read(raw.PtrCast[u64, fn(uint, uint, *u8, *u8): i32](raw.OfMut(mut wv)))
        freechain := raw.Read(raw.PtrCast[u64, fn(uint)](raw.OfMut(mut wfc)))
        freectx := raw.Read(raw.PtrCast[u64, fn(uint): i32](raw.OfMut(mut wfx)))
        closestore := raw.Read(raw.PtrCast[u64, fn(uint, u32): i32](raw.OfMut(mut wcs)))
        k0 := int(chain[0]) | (int(chain[1]) << 8) | (int(chain[2]) << 16) | (int(chain[3]) << 24)
        leaf := create(CertEncoding, raw.Offset(raw.Data(chain), 4), Trunc[u32](k0))
        if leaf == 0 {
            return LastError()
        }
        store := open(StoreMemory, 0, 0, StoreDeferClose, 0)
        if store == 0 {
            r := LastError()
            _ = freectx(leaf)
            return r
        }
        var at := 4 + k0
        for _ in 1..n {
            k := int(chain[at]) | (int(chain[at + 1]) << 8) | (int(chain[at + 2]) << 16) | (int(chain[at + 3]) << 24)
            if add(store, CertEncoding, raw.Offset(raw.Data(chain), at + 4), Trunc[u32](k), AddAlways, 0) == 0 {
                r := LastError()
                _ = closestore(store, 0)
                _ = freectx(leaf)
                return r
            }
            at += 4 + k
        }
        // CERT_CHAIN_PARA, the whole 96 bytes: cbSize, CERT_USAGE_MATCH
        // (AND, one usage: serverAuth) at 8, no issuance policy, no
        // retrieval timeout, freshness or resync, and at 80
        // pStrongSignPara: CERT_STRONG_SIGN_PARA (16 bytes:
        // cbSize, dwInfoChoice CERT_STRONG_SIGN_OID_INFO_CHOICE, pszOID)
        // naming szOID_CERT_STRONG_SIGN_OS_1, so the platform sets
        // CERT_TRUST_HAS_WEAK_SIGNATURE on a SHA-1, MD5 or MD2 signature of
        // its own path whatever the machine's settings say; and at 88
        // dwStrongSignFlags CERT_CHAIN_STRONG_SIGN_DISABLE_END_CHECK_FLAG,
        // so the end certificate's own public key is not held to OS_1's
        // key rules (RSA of 2047 bits or more, ECDSA), under which an
        // Ed25519 or RSA-1024 leaf key is weak. With
        // it the platform's path agrees with the program-pool path:
        // x509.Verify does not hold the leaf's key to a size on either
        // path; Certificate.PublicKey refuses an RSA key under 2048 bits,
        // and tls.Client calls it.
        oid := raw.StrData("1.3.6.1.5.5.7.3.1\u{0}")
        var oids: [1]u64 = [u64(raw.Addr(oid))]
        var strong: [2]u64 = [0; 2]
        sg := raw.PtrCast[u64, u8](raw.DataMut(mut strong[..]))
        Put32(sg, 0, 16)
        Put32(sg, 4, StrongSignOid)
        Put64(sg, 8, u64(raw.Addr(raw.StrData("1.3.6.1.4.1.311.72.1.1\u{0}"))))
        var para: [12]u64 = [0; 12]
        pp := raw.PtrCast[u64, u8](raw.DataMut(mut para[..]))
        Put32(pp, 0, 96)
        Put32(pp, 16, 1)
        Put64(pp, 24, u64(raw.Addr(raw.Data(oids[..]))))
        Put64(pp, 80, u64(raw.Addr(sg)))
        Put32(pp, 88, StrongSignNoEndCheck)
        // FILETIME of now; now / 100 cannot overflow the sum.
        var ft: [1]i64 = [now / 100 + CertEpoch]
        var ctx: uint = 0
        if get(0, leaf, raw.PtrCast[i64, u8](raw.DataMut(mut ft[..])), store, pp, flags, 0, raw.OfMut(mut ctx)) == 0 {
            r := LastError()
            _ = closestore(store, 0)
            _ = freectx(leaf)
            return r
        }
        // SSL_EXTRA_CERT_CHAIN_POLICY_PARA (24 bytes), CERT_CHAIN_POLICY_PARA
        // (16) and CERT_CHAIN_POLICY_STATUS (24).
        var ssl: [3]u64 = [0; 3]
        sp := raw.PtrCast[u64, u8](raw.DataMut(mut ssl[..]))
        Put32(sp, 0, 24)
        Put32(sp, 4, AuthServer)
        if host.Len() > 0 {
            Put64(sp, 16, u64(raw.Addr(raw.Data(host16[..]))))
        }
        var pol: [2]u64 = [0; 2]
        ppol := raw.PtrCast[u64, u8](raw.DataMut(mut pol[..]))
        Put32(ppol, 0, 16)
        Put64(ppol, 8, u64(raw.Addr(sp)))
        var st: [3]u64 = [0; 3]
        pst := raw.PtrCast[u64, u8](raw.DataMut(mut st[..]))
        Put32(pst, 0, 24)
        ok := verify(PolicySsl, ctx, ppol, pst)
        var r := LastError()
        if ok != 0 {
            trust := Get32(raw.FromAddr[u8](ctx), 4)
            r = OsResult(value: i64(Get32(pst, 4)), err: core.SysErr.None, os: i64(trust))
        }
        freechain(ctx)
        _ = closestore(store, 0)
        _ = freectx(leaf)
        return r
    }
}

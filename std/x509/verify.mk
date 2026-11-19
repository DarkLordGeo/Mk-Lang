// Why: a TLS client must decide whether a server's chain leads to a trusted
// root, names the host, and is in date, against a peer that may send a
// hostile chain.
//
// What: Verify, with BuildPath (the search), VerifyWith, Node, IssuerHint,
// Twin and TimeBefore.
//
// How: RFC 5280 path building and validation, with no policy processing or
// revocation, by Go's crypto/x509 buildChains made iterative: a depth-first
// walk over candidate issuers, the path and each level's next candidate in
// fixed arrays, so the walk uses O(1) stack. More below.

// Checked: name constraints, key usage, basic constraints and validity.
// Candidates come from the pool first, then the chain. Every signature
// check counts, and the 101st ends the walk with TooComplex, as Go's
// maxChainSignatureChecks does, which also bounds a path's length. A
// candidate's signature is checked before anything else of it is read, and
// name constraints only when a root closes a path, over the whole path
// (Go's checkChainConstraints), so an untrusted certificate the peer sent
// costs one signature check at most; their work shares one budget.

import std.time

// MaxPath is the most certificates one path holds, the leaf and the root
// included: each step takes a signature check, so MaxChecks + 1 is never
// reached.
const MaxPath: int = 101

// MaxChecks is the most signature checks one Verify makes.
const MaxChecks: int = 100

// The hint codes: why the last issuer candidate was refused.
const HintNone: int = 0
const HintExpired: int = 1
const HintUsage: int = 2
const HintSignature: int = 3
const HintUnsupported: int = 4

/// Checks that chain, the leaf first and then any intermediates in any
/// order, is a valid certificate chain for a TLS server named host at the
/// time now, ending at a certificate of roots. It builds paths by RFC 5280,
/// with name constraints, key usage, basic constraints and validity, and
/// without policy processing or revocation. O(100 signature checks +
/// (pool + chain) per step + (k + names) log k per constrained issuer of a
/// complete path); allocates the sorted constraint sets of one issuer at a
/// time in a sub-arena of a, freed after, and the platform's request.
///
/// The leaf: host must match one of its subjectAltName names; the subject's
/// common name is never used. An IP address, bare or in brackets, matches
/// its IP names; an empty host is not checked. Where present, its key
/// usage must allow digitalSignature and its extended key usage serverAuth
/// or any.
///
/// The path: every certificate valid at now; every issuer a CA, with
/// keyCertSign where key usage is present and a path length that allows
/// what is below it; every name below an issuer within its name
/// constraints; every signature by SHA-256, -384 or -512, or Ed25519, and
/// verifying. SHA-1 and MD5 signatures are refused. Under a URI name
/// constraint, a URI name is matched only when every URL reader reads its
/// authority alike: RFC 3986 bytes, one `@`, no `%` or `\` in the host, no
/// control byte; any other is refused. A weak signature the path does not
/// use, such as an unused SHA-1 cross-signature, or a root's own, is not
/// checked. Verify does not check the size of the leaf's key: tls.Client
/// calls Certificate.PublicKey, which refuses an RSA key under 2048 bits.
///
/// With the pool SystemRoots gives on Windows, the leaf is checked as
/// above and the chain is handed to the platform's check, on a pool thread
/// through io. The platform is asked for strong signatures
/// (`szOID_CERT_STRONG_SIGN_OS_1`), so these are Unsupported: a SHA-1, MD5
/// or MD2 signature on the leaf or on any certificate of the path the
/// platform builds; an RSA key shorter than 2047 bits on a certificate
/// above the leaf on that path; and an Ed25519 key above the leaf, which
/// the platform cannot check. A program's own pool accepts that last one.
///
/// Fails with NameMismatch, BadUsage, Expired, BadSignature, Unsupported,
/// UnknownAuthority (no path, or an empty chain), TooComplex (the 101st
/// signature check, 2^20 units of name-constraint work, or a chain over 16
/// certificates on the platform's path), Canceled (only before the
/// platform's check starts) and OutOfMemory.
export Verify(a: &Arena, io: Io, chain: []Certificate, roots: &Pool, now: time.Time, host: str): Error!void {
    try VerifyWith(a, io, chain, roots, now, host, false)
}

// VerifyWith is Verify; cached (tests only) asks the platform's path for
// retrieval from the local cache only.
VerifyWith(a: &Arena, io: Io, chain: []Certificate, roots: &Pool, now: time.Time, host: str, cached: bool): Error!void {
    if chain.Len() == 0 {
        fail UnknownAuthority
    }
    leaf := &chain[0]
    if host.Len() > 0 && !HostMatches(leaf, host) {
        fail NameMismatch
    }
    if leaf.Has(FlagCritical) {
        fail Unsupported
    }
    if leaf.Has(FlagKeyUsage) && (leaf.key_usage & UsageDigitalSignature) == 0 {
        fail BadUsage
    }
    if leaf.Has(FlagEku) && !leaf.Has(FlagEkuServer) && !leaf.Has(FlagEkuAny) {
        fail BadUsage
    }
    // RFC 5280 4.2.1.10: only a CA carries name constraints.
    if leaf.Has(FlagConstraints) && !leaf.Has(FlagCa) {
        fail BadUsage
    }
    if roots.system {
        // A weak signature is refused on every certificate
        // a path checks. The leaf's is refused here, since a leaf the
        // platform trusts as a root of its store is checked by no path
        // of its own; every other certificate's is the platform's, which
        // is asked for strong signatures and flags a weak one on the path
        // it builds whatever the machine's settings (Platform), so a weak
        // certificate the path does not use is not refused.
        if leaf.sig_alg == AlgWeak {
            fail Unsupported
        }
        try Platform(a, io, chain, now, cached)
        return
    }
    if TimeBefore(now, leaf.valid_from) || TimeBefore(leaf.valid_to, now) {
        fail Expired
    }
    for i in 0..roots.certs.Len() {
        if Same(roots.certs[i].data, leaf.data) {
            return
        }
    }
    try BuildPath(a, chain, roots.certs, now)
}

// TimeBefore is whether a is before b.
TimeBefore(a: time.Time, b: time.Time): bool {
    return a.UnixNanos() < b.UnixNanos()
}

// Node is the certificate id names: 0 to n - 1 the chain's, n on the
// pool's.
Node(chain: []Certificate, pool: []Certificate, id: int): &Certificate from chain, pool {
    if id < chain.Len() {
        return &chain[id]
    }
    return &pool[id - chain.Len()]
}

// IssuerHint is why y may not issue a certificate with depth intermediates
// below it, HintNone when it may: an unhandled critical extension, a
// validity that excludes now, an intermediate that is not a CA or lacks
// keyCertSign, a path length exceeded, or an extended key usage without
// serverAuth. A root is trusted as a CA by being in the pool.
IssuerHint(y: &Certificate, root: bool, depth: int, now: time.Time): int {
    if y.Has(FlagCritical) {
        return HintUnsupported
    }
    if TimeBefore(now, y.valid_from) || TimeBefore(y.valid_to, now) {
        return HintExpired
    }
    if !root && (!y.Has(FlagBasic) || !y.Has(FlagCa)) {
        return HintUsage
    }
    if !root && y.Has(FlagKeyUsage) && (y.key_usage & UsageKeyCertSign) == 0 {
        return HintUsage
    }
    if y.Has(FlagBasic) && y.path_len >= 0 && depth > y.path_len {
        return HintUsage
    }
    if y.Has(FlagEku) && !y.Has(FlagEkuServer) && !y.Has(FlagEkuAny) {
        return HintUsage
    }
    return HintNone
}

// Twin is whether a and b are the same certificate for path building:
// the same subject, key and subjectAltName, as Go's alreadyInChain.
Twin(a: &Certificate, b: &Certificate): bool {
    return Same(a.SubjectDer(), b.SubjectDer()) && Same(a.Spki(), b.Spki()) && Same(a.San(), b.San())
}

// BuildPath searches for a path from chain[0] to a certificate of pool.
BuildPath(a: &Arena, chain: []Certificate, pool: []Certificate, now: time.Time): Error!void {
    var path: [101]int = [0; 101]
    var next: [101]int = [0; 101]
    var depth := 0
    var checks := 0
    var budget := MaxComparisons
    var hint := HintNone
    // named: a complete path broke a name constraint (Go reports that
    // first); deep: a valid issuer could not be stepped to.
    var named := false
    var deep := false
    n := chain.Len()
    total := pool.Len() + n - 1
    for {
        if next[depth] == total {
            if depth == 0 {
                break
            }
            depth -= 1
            continue
        }
        j := next[depth]
        next[depth] += 1
        // The pool's certificates first, then the chain's after the leaf.
        id := if j < pool.Len() { n + j } else { j - pool.Len() + 1 }
        root := id >= n
        x := Node(chain, pool, path[depth])
        y := Node(chain, pool, id)
        if !Same(y.SubjectDer(), x.IssuerDer()) {
            continue
        }
        var again := false
        for k in 0..depth + 1 {
            if Twin(Node(chain, pool, path[k]), y) {
                again = true
            }
        }
        if again {
            continue
        }
        h := IssuerHint(y, root, depth, now)
        if h != HintNone {
            hint = h
            continue
        }
        if checks == MaxChecks {
            fail TooComplex
        }
        checks += 1
        CheckSig(x, y) else e {
            hint = match e {
                Unsupported { HintUnsupported }
                _ { HintSignature }
            }
            continue
        }
        if depth + 2 > MaxPath {
            deep = true
            continue
        }
        path[depth + 1] = id
        if root {
            r := try PathNames(a, chain, pool, path[0..depth + 2], mut budget)
            if r == NameTooMany {
                fail TooComplex
            }
            if r == NameOk {
                return
            }
            named = true
            continue
        }
        depth += 1
        next[depth] = 0
    }
    if named {
        fail BadUsage
    }
    if deep {
        fail TooComplex
    }
    if hint == HintExpired {
        fail Expired
    }
    if hint == HintUsage {
        fail BadUsage
    }
    if hint == HintSignature {
        fail BadSignature
    }
    if hint == HintUnsupported {
        fail Unsupported
    }
    fail UnknownAuthority
}

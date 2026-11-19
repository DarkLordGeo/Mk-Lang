// Why: a certificate names its signature algorithm by OID, and Verify must
// know which ones it checks, which it refuses and which it does not know.
//
// What: SigAlgOf, the algorithm of an AlgorithmIdentifier, with HashOid,
// HashAi, PssOf and AlgHash.
//
// How: RSA PKCS#1 v1.5 and RSASSA-PSS with SHA-256, -384 and -512, ECDSA
// with the same hashes, and Ed25519 (RFC 5280 4.1.1.2). SHA-1 and MD5 are
// named so Verify can refuse them; any other algorithm is Unknown, which
// Parse accepts and Verify refuses.

// The RFCs: RSASSA-PSS RFC 4055 and RFC 8017, ECDSA RFC 5758, Ed25519 RFC
// 8410. PSS is held to Go's three buckets: the MGF1 hash is the message
// hash, the salt length is the hash length and the trailer field is 1.

import std.crypto

// The SigAlg codes of Certificate.sig_alg.
const AlgUnknown: u8 = 0
const AlgRsaSha256: u8 = 1
const AlgRsaSha384: u8 = 2
const AlgRsaSha512: u8 = 3
const AlgPssSha256: u8 = 4
const AlgPssSha384: u8 = 5
const AlgPssSha512: u8 = 6
const AlgEcdsaSha256: u8 = 7
const AlgEcdsaSha384: u8 = 8
const AlgEcdsaSha512: u8 = 9
const AlgEd25519: u8 = 10
const AlgWeak: u8 = 11

// The object identifiers' content octets (X.690 8.19).
// sha256WithRSAEncryption 1.2.840.113549.1.1.11, sha384 .12, sha512 .13,
// RSASSA-PSS .10, sha1WithRSAEncryption .5, md5WithRSAEncryption .4,
// md2WithRSAEncryption .2, mgf1 .8 (RFC 8017 A.2).
const OidRsaSha256: [9]u8 = [0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0b]
const OidRsaSha384: [9]u8 = [0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0c]
const OidRsaSha512: [9]u8 = [0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0d]
const OidRsaPss: [9]u8 = [0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0a]
const OidRsaSha1: [9]u8 = [0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x05]
const OidRsaMd5: [9]u8 = [0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x04]
const OidRsaMd2: [9]u8 = [0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x02]
const OidMgf1: [9]u8 = [0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x08]
// ecdsa-with-SHA256 1.2.840.10045.4.3.2, SHA384 .3, SHA512 .4,
// ecdsa-with-SHA1 1.2.840.10045.4.1 (RFC 5758 3.2, RFC 3279 2.2.3).
const OidEcdsaSha256: [8]u8 = [0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x02]
const OidEcdsaSha384: [8]u8 = [0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x03]
const OidEcdsaSha512: [8]u8 = [0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x04]
const OidEcdsaSha1: [7]u8 = [0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x01]
// id-Ed25519 1.3.101.112 (RFC 8410 3).
const OidEd25519: [3]u8 = [0x2b, 0x65, 0x70]
// id-sha256 2.16.840.1.101.3.4.2.1, sha384 .2, sha512 .3 (RFC 5754).
const OidSha256: [9]u8 = [0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01]
const OidSha384: [9]u8 = [0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x02]
const OidSha512: [9]u8 = [0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x03]

// SigAlgOf is the code of the AlgorithmIdentifier ai: SEQUENCE { OID,
// parameters ANY OPTIONAL }. Parameters are read for PSS, and must be
// absent for Ed25519; an algorithm std.x509 does not know, or one in a
// form it does not take, is AlgUnknown, not a fault. An OID Go's parseAI
// does not read (not minimal, cut off, an arc past 2^31) is Malformed.
SigAlgOf(der: []u8, ai: &El): Malformed!u8 {
    var c := Inside(der, ai)
    oe := try c.Want(6, false)
    if !OidValid(Content(der, &oe)) {
        fail Malformed(offset: oe.at)
    }
    var params := oe
    var has := false
    if c.More() {
        params = try c.Next()
        has = true
    }
    try c.Finish()
    o := Content(der, &oe)
    if Same(o, OidRsaSha256[..]) {
        return AlgRsaSha256
    }
    if Same(o, OidRsaSha384[..]) {
        return AlgRsaSha384
    }
    if Same(o, OidRsaSha512[..]) {
        return AlgRsaSha512
    }
    if Same(o, OidEcdsaSha256[..]) {
        return AlgEcdsaSha256
    }
    if Same(o, OidEcdsaSha384[..]) {
        return AlgEcdsaSha384
    }
    if Same(o, OidEcdsaSha512[..]) {
        return AlgEcdsaSha512
    }
    if Same(o, OidEd25519[..]) {
        // RFC 8410 3: the parameters MUST be absent; Go's
        // getSignatureAlgorithmFromAI makes any other form unknown.
        return if has { AlgUnknown } else { AlgEd25519 }
    }
    if Same(o, OidRsaSha1[..]) || Same(o, OidRsaMd5[..]) || Same(o, OidRsaMd2[..]) || Same(o, OidEcdsaSha1[..]) {
        return AlgWeak
    }
    if Same(o, OidRsaPss[..]) && has {
        return try PssOf(der, &params)
    }
    return AlgUnknown
}

// HashOid is 1, 2 or 3 for the OID of SHA-256, -384 or -512, else 0.
HashOid(o: []u8): int {
    if Same(o, OidSha256[..]) {
        return 1
    }
    if Same(o, OidSha384[..]) {
        return 2
    }
    if Same(o, OidSha512[..]) {
        return 3
    }
    return 0
}

// HashAi is HashOid of the AlgorithmIdentifier e, whose parameters must be
// absent or NULL; 0 for anything else.
HashAi(der: []u8, e: &El): int {
    if e.class != 0 || e.tag != 16 || !e.cons {
        return 0
    }
    var c := Inside(der, e)
    oe := c.Want(6, false) else {
        return 0
    }
    if c.More() {
        p := c.Next() else {
            return 0
        }
        if p.class != 0 || p.tag != 5 || p.cons || p.end != p.at {
            return 0
        }
    }
    if c.More() {
        return 0
    }
    return HashOid(Content(der, &oe))
}

// PssOf is the code of RSASSA-PSS-params p (RFC 8017 A.2.3): [0] hash,
// [1] MGF1 with the same hash, [2] salt length equal to the hash's, [3]
// trailer 1 or absent; AlgUnknown for any other choice. Each tag is
// EXPLICIT, so constructed: a primitive [0], [1] or [2] is AlgUnknown, as
// Go's encoding/asn1 fails them, and a primitive [3], which Go skips as
// absent, is Malformed.
PssOf(der: []u8, p: &El): Malformed!u8 {
    if p.class != 0 || p.tag != 16 || !p.cons {
        return AlgUnknown
    }
    var c := Inside(der, p)
    if !c.Peek(2, 0) {
        return AlgUnknown
    }
    h0 := try c.Next()
    if !h0.cons {
        return AlgUnknown
    }
    var hc := Inside(der, &h0)
    ha := try hc.Next()
    try hc.Finish()
    h := HashAi(der, &ha)
    if h == 0 || !c.Peek(2, 1) {
        return AlgUnknown
    }
    m0 := try c.Next()
    if !m0.cons {
        return AlgUnknown
    }
    var mc := Inside(der, &m0)
    mgf := try mc.Want(16, true)
    try mc.Finish()
    var gc := Inside(der, &mgf)
    gid := try gc.Want(6, false)
    gh := try gc.Next()
    try gc.Finish()
    if !Same(Content(der, &gid), OidMgf1[..]) || HashAi(der, &gh) != h || !c.Peek(2, 2) {
        return AlgUnknown
    }
    s0 := try c.Next()
    if !s0.cons {
        return AlgUnknown
    }
    var sc := Inside(der, &s0)
    se := try sc.Want(2, false)
    try sc.Finish()
    salt := SmallUint(Content(der, &se))
    if c.Peek(2, 3) {
        t0 := try c.Next()
        if !t0.cons {
            fail Malformed(offset: t0.start)
        }
        var tc := Inside(der, &t0)
        te := try tc.Want(2, false)
        try tc.Finish()
        if SmallUint(Content(der, &te)) != 1 {
            return AlgUnknown
        }
    }
    try c.Finish()
    if h == 1 && salt == 32 {
        return AlgPssSha256
    }
    if h == 2 && salt == 48 {
        return AlgPssSha384
    }
    if h == 3 && salt == 64 {
        return AlgPssSha512
    }
    return AlgUnknown
}

// AlgHash is the hash an algorithm signs with.
AlgHash(code: u8): crypto.HashKind {
    if code == AlgRsaSha384 || code == AlgPssSha384 || code == AlgEcdsaSha384 {
        return crypto.HashKind.Sha384
    }
    if code == AlgRsaSha512 || code == AlgPssSha512 || code == AlgEcdsaSha512 {
        return crypto.HashKind.Sha512
    }
    return crypto.HashKind.Sha256
}

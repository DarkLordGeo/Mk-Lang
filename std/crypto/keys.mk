// Why: keys arrive as DER from certificates, PEM files and other tools,
// and a parser of hostile input must never panic or recurse without bound.
//
// What: PublicKey, PrivateKey, ParsePublicKey and ParsePrivateKey, with the
// algorithm OIDs and the readers KeyAlgorithm, KeyEc and KeyRsa.
//
// How: SubjectPublicKeyInfo (RFC 5280 4.1.2.7; EC keys RFC 5480, RSA keys RFC
// 8017 A.1.1, Ed25519 RFC 8410) and a bare PKCS#1 RSAPublicKey for public
// keys; unencrypted PKCS#8 (RFC 5208, RFC 5958's version 1 included) holding
// a SEC 1 EC key (RFC 5915) or an Ed25519 seed (RFC 8410 7), and a bare SEC 1
// ECPrivateKey with its curve, for private keys. PEM is the caller's:
// encoding.ParsePem gives the DER. Every element is read through
// std.encoding's DER reader, which refuses BER's other forms, takes one
// element at a time and nests at most 32 deep, so parsing holds O(1) memory
// and stack whatever the input (no recursion here either), and never panics
// on input.
//
// Errors: bytes that are not the structure's DER are BadEncoding; an
// encrypted PKCS#8 (EncryptedPrivateKeyInfo) is Encrypted; a well-formed
// key of an algorithm or curve std.crypto does not take (an RSA private
// key, a P-384 private key, an unknown OID), an EC point off its curve, a
// scalar out of range, or an RSA key RsaPublic.New refuses is BadKey.

import std.encoding

/// PublicKey is a parsed public key: an EC point with its curve (an
/// uncompressed SEC 1 point, a view of the parsed bytes), an RSA key, or an
/// Ed25519 key.
export enum PublicKey {
    /// An EC public key: its curve and its uncompressed SEC 1 point, a view of
    /// the parsed bytes.
    Ec(curve: Curve, point: []u8)
    /// An RSA public key.
    Rsa(key: RsaPublic)
    /// An Ed25519 public key, 32 bytes.
    Ed25519(key: [32]u8)
}

/// PrivateKey is a parsed private key: a P-256 secret, or an Ed25519 seed,
/// which Ed25519.FromSeed turns into a secret key.
export enum PrivateKey {
    /// A P-256 secret.
    Ec(secret: EcSecret)
    /// An Ed25519 seed, which Ed25519.FromSeed turns into a secret key.
    Ed25519(seed: [32]u8)
}

// The object identifiers' content octets (X.690 8.19).
// id-ecPublicKey, 1.2.840.10045.2.1 (RFC 5480 2.1.1).
const OidEcPublicKey: [7]u8 = [0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01]
// secp256r1, 1.2.840.10045.3.1.7 (RFC 5480 2.1.1.1).
const OidP256: [8]u8 = [0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07]
// secp384r1, 1.3.132.0.34.
const OidP384: [5]u8 = [0x2b, 0x81, 0x04, 0x00, 0x22]
// rsaEncryption, 1.2.840.113549.1.1.1 (RFC 8017 A.1).
const OidRsa: [9]u8 = [0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x01]
// id-Ed25519, 1.3.101.112 (RFC 8410 3).
const OidEd25519: [3]u8 = [0x2b, 0x65, 0x70]

// KeyIs reports whether v is the universal element with tag number tag
// (constructed for a SEQUENCE, primitive otherwise).
KeyIs(v: &encoding.DerValue, tag: u32): bool {
    return v.class == 0 && v.tag == tag && v.constructed == (tag == 16)
}

// KeySame reports whether b holds the bytes of want (public data).
KeySame(b: []u8, want: []u8): bool {
    if b.Len() != want.Len() {
        return false
    }
    for i in 0..b.Len() {
        if b[i] != want[i] {
            return false
        }
    }
    return true
}

// KeyAlgorithm is an AlgorithmIdentifier's kind (0 EC, 1 RSA, 2 Ed25519)
// and, for EC, the curve's kind (0 P-256, 1 P-384). Fails with
// BadEncoding when v is not an AlgorithmIdentifier, and with BadKey for
// an algorithm or curve std.crypto does not take, or RSA's parameters not
// NULL, or Ed25519's present.
KeyAlgorithm(v: &encoding.DerValue): Error!(kind: int, curve: int) {
    if !KeyIs(v, 16) {
        fail BadEncoding
    }
    var it := v.Children()
    n1 := it.Next() else {
        fail BadEncoding
    }
    oid := n1 else {
        fail BadEncoding
    }
    if !KeyIs(&oid, 6) {
        fail BadEncoding
    }
    n2 := it.Next() else {
        fail BadEncoding
    }
    n3 := it.Next() else {
        fail BadEncoding
    }
    if n3.IsSome() {
        fail BadEncoding
    }
    if KeySame(oid.content, OidEcPublicKey[..]) {
        p := n2 else {
            fail BadKey
        }
        if !KeyIs(&p, 6) {
            fail BadKey
        }
        if KeySame(p.content, OidP256[..]) {
            return (kind: 0, curve: 0)
        }
        if KeySame(p.content, OidP384[..]) {
            return (kind: 0, curve: 1)
        }
        fail BadKey
    }
    if KeySame(oid.content, OidRsa[..]) {
        p := n2 else {
            fail BadKey
        }
        if !KeyIs(&p, 5) || p.content.Len() != 0 {
            fail BadKey
        }
        return (kind: 1, curve: 0)
    }
    if KeySame(oid.content, OidEd25519[..]) {
        if n2.IsSome() {
            fail BadKey
        }
        return (kind: 2, curve: 0)
    }
    fail BadKey
}

// KeyUint is the non-negative DER INTEGER v's magnitude, without the zero
// byte DER puts before a set top bit. Fails with BadEncoding when v is
// not an INTEGER or is negative.
KeyUint(v: &encoding.DerValue): Error![]u8 from v.content {
    if !KeyIs(v, 2) {
        fail BadEncoding
    }
    b := v.Integer() else {
        fail BadEncoding
    }
    if b[0] >= 0x80 {
        fail BadEncoding
    }
    if b.Len() > 1 && b[0] == 0 {
        return b[1..b.Len()]
    }
    return b
}

// KeyRsa is the PKCS#1 RSAPublicKey v, SEQUENCE { modulus, exponent }.
KeyRsa(v: &encoding.DerValue): Error!RsaPublic from v.content {
    if !KeyIs(v, 16) {
        fail BadEncoding
    }
    var it := v.Children()
    n1 := it.Next() else {
        fail BadEncoding
    }
    nv := n1 else {
        fail BadEncoding
    }
    n2 := it.Next() else {
        fail BadEncoding
    }
    ev := n2 else {
        fail BadEncoding
    }
    n3 := it.Next() else {
        fail BadEncoding
    }
    if n3.IsSome() {
        fail BadEncoding
    }
    n := try KeyUint(&nv)
    e := try KeyUint(&ev)
    if e.Len() > 8 {
        fail BadKey
    }
    var x: u64 = 0
    for i in 0..e.Len() {
        x = (x << 8) | u64(e[i])
    }
    return try RsaPublic.New(n, x)
}

// KeyBits is a BIT STRING's whole bytes: BadEncoding when v is not a BIT
// STRING or has unused bits.
KeyBits(v: &encoding.DerValue): Error![]u8 from v.content {
    if !KeyIs(v, 3) || v.content.Len() == 0 || v.content[0] != 0 {
        fail BadEncoding
    }
    return v.content[1..v.content.Len()]
}

/// Reads one DER public key: a SubjectPublicKeyInfo of an EC (P-256 or
/// P-384, uncompressed point), RSA or Ed25519 key, or a PKCS#1
/// RSAPublicKey, with nothing after it. An EC point and an RSA modulus are
/// views of der. Fails with BadEncoding when der is not that DER, and with
/// BadKey for another algorithm or curve, an EC point off its curve or not
/// uncompressed, or an RSA key RsaPublic.New refuses. Never panics on
/// input. O(der) plus one point check; allocates nothing.
export ParsePublicKey(der: []u8): Error!PublicKey from der {
    t := encoding.DerRead(der) else {
        fail BadEncoding
    }
    if t.rest.Len() != 0 || !KeyIs(&t.value, 16) {
        fail BadEncoding
    }
    var it := t.value.Children()
    n1 := it.Next() else {
        fail BadEncoding
    }
    first := n1 else {
        fail BadEncoding
    }
    if KeyIs(&first, 2) {
        return PublicKey.Rsa(key: try KeyRsa(&t.value))
    }
    n2 := it.Next() else {
        fail BadEncoding
    }
    bits := n2 else {
        fail BadEncoding
    }
    n3 := it.Next() else {
        fail BadEncoding
    }
    if n3.IsSome() {
        fail BadEncoding
    }
    alg := try KeyAlgorithm(&first)
    key := try KeyBits(&bits)
    if alg.kind == 0 {
        if alg.curve == 0 {
            if P256PtFromBytes(key).IsNone() {
                fail BadKey
            }
            return PublicKey.Ec(curve: Curve.P256, point: key)
        }
        if P384PtFromBytes(key).IsNone() {
            fail BadKey
        }
        return PublicKey.Ec(curve: Curve.P384, point: key)
    }
    if alg.kind == 1 {
        r := encoding.DerRead(key) else {
            fail BadEncoding
        }
        if r.rest.Len() != 0 {
            fail BadEncoding
        }
        return PublicKey.Rsa(key: try KeyRsa(&r.value))
    }
    if key.Len() != 32 {
        fail BadEncoding
    }
    var k: [32]u8 = [0; 32]
    (mut k[..]).CopyFrom(key)
    return PublicKey.Ed25519(key: k)
}

// KeyEc is the SEC 1 ECPrivateKey v (RFC 5915 3): SEQUENCE { version 1,
// privateKey OCTET STRING, [0] parameters OPTIONAL, [1] publicKey
// OPTIONAL }. curve is the PKCS#8 algorithm's curve (0 P-256, 1 P-384),
// or -1 when v stands alone and names its own; when both are present
// they must agree. The scalar may be shorter than 32 bytes (left-padded)
// or longer with leading zero bytes (stripped; a non-zero one is
// BadEncoding), as Go's crypto/x509 reads both though SEC 1 allows
// neither. The padding is OR-ed into one byte and
// tested once, so no branch reads the scalar.
KeyEc(v: &encoding.DerValue, curve: int): Error!EcSecret {
    if !KeyIs(v, 16) {
        fail BadEncoding
    }
    var it := v.Children()
    n1 := it.Next() else {
        fail BadEncoding
    }
    ver := n1 else {
        fail BadEncoding
    }
    vb := try KeyUint(&ver)
    if vb.Len() != 1 || vb[0] != 1 {
        fail BadEncoding
    }
    n2 := it.Next() else {
        fail BadEncoding
    }
    oct := n2 else {
        fail BadEncoding
    }
    if !KeyIs(&oct, 4) {
        fail BadEncoding
    }
    var c := curve
    var last: u32 = 0
    var more := true
    for more {
        n := it.Next() else {
            fail BadEncoding
        }
        x := n else {
            more = false
            continue
        }
        if x.class != 2 || !x.constructed || x.tag < last || x.tag > 1 {
            fail BadEncoding
        }
        last = x.tag + 1
        if x.tag == 0 {
            var pi := x.Children()
            pn := pi.Next() else {
                fail BadEncoding
            }
            p := pn else {
                fail BadEncoding
            }
            pr := pi.Next() else {
                fail BadEncoding
            }
            if pr.IsSome() || !KeyIs(&p, 6) {
                fail BadEncoding
            }
            named := if KeySame(p.content, OidP256[..]) { 0 } else if KeySame(p.content, OidP384[..]) { 1 } else { 2 }
            if named == 2 || (c >= 0 && c != named) {
                fail BadKey
            }
            c = named
        } else {
            // [1] holds the public key's BIT STRING, read for its form only:
            // the public key is recomputed from the scalar, as Go does.
            var ki := x.Children()
            kn := ki.Next() else {
                fail BadEncoding
            }
            k := kn else {
                fail BadEncoding
            }
            kr := ki.Next() else {
                fail BadEncoding
            }
            if kr.IsSome() {
                fail BadEncoding
            }
            _ = try KeyBits(&k)
        }
    }
    if c != 0 {
        // P-384 has no secrets in std.crypto; no curve is BadKey.
        fail BadKey
    }
    var d := oct.content
    if d.Len() > 32 {
        lead := d.Len() - 32
        var pad: u8 = 0
        for i in 0..lead {
            pad = pad | d[i]
        }
        if pad != 0 {
            fail BadEncoding
        }
        d = d[lead..d.Len()]
    }
    var b: [32]u8 = [0; 32]
    (mut b[32 - d.Len()..32]).CopyFrom(d)
    s := EcSecret.FromBytes(Curve.P256, b[..]) else {
        Wipe(mut b[..])
        fail BadKey
    }
    Wipe(mut b[..])
    return s
}

/// Reads one DER private key: an unencrypted PKCS#8 PrivateKeyInfo (version
/// 0, or 1 with a public key) of a P-256 or Ed25519 key, or a SEC 1
/// ECPrivateKey naming P-256, with nothing after it. A SEC 1 scalar may be
/// shorter than 32 bytes, or longer with leading zero bytes, which are
/// skipped.
///
/// Fails with Encrypted for a PKCS#8 EncryptedPrivateKeyInfo; with
/// BadEncoding when der is not one of these, a PKCS#1 RSAPrivateKey
/// included, or when a scalar's excess leading byte is not zero; and with
/// BadKey for an RSA or P-384 key in PKCS#8, another algorithm or curve, or
/// a scalar outside [1, n - 1]. The secret is copied out of der, so der may
/// be wiped after. Never panics on input. O(der); allocates nothing.
export ParsePrivateKey(der: []u8): Error!PrivateKey {
    t := encoding.DerRead(der) else {
        fail BadEncoding
    }
    if t.rest.Len() != 0 || !KeyIs(&t.value, 16) {
        fail BadEncoding
    }
    var it := t.value.Children()
    n1 := it.Next() else {
        fail BadEncoding
    }
    first := n1 else {
        fail BadEncoding
    }
    n2 := it.Next() else {
        fail BadEncoding
    }
    second := n2 else {
        fail BadEncoding
    }
    // EncryptedPrivateKeyInfo (RFC 5208 6): SEQUENCE { algorithm, OCTET
    // STRING }.
    if KeyIs(&first, 16) && KeyIs(&second, 4) {
        fail Encrypted
    }
    // SEC 1: version 1 then the scalar's OCTET STRING.
    if KeyIs(&second, 4) {
        return PrivateKey.Ec(secret: try KeyEc(&t.value, -1))
    }
    ver := try KeyUint(&first)
    if ver.Len() != 1 || ver[0] > 1 {
        fail BadEncoding
    }
    n3 := it.Next() else {
        fail BadEncoding
    }
    inner := n3 else {
        fail BadEncoding
    }
    if !KeyIs(&inner, 4) {
        fail BadEncoding
    }
    // [0] attributes and [1] publicKey (RFC 5958 2) may follow, in order.
    var last: u32 = 0
    var more := true
    for more {
        n := it.Next() else {
            fail BadEncoding
        }
        x := n else {
            more = false
            continue
        }
        if x.class != 2 || x.tag < last || x.tag > 1 || (x.tag == 1 && ver[0] == 0) {
            fail BadEncoding
        }
        // [0] is a SET OF attributes; [1] an implicit BIT STRING.
        if x.constructed != (x.tag == 0) || (x.tag == 1 && (x.content.Len() == 0 || x.content[0] > 7)) {
            fail BadEncoding
        }
        last = x.tag + 1
    }
    alg := try KeyAlgorithm(&second)
    if alg.kind == 1 {
        fail BadKey
    }
    r := encoding.DerRead(inner.content) else {
        fail BadEncoding
    }
    if r.rest.Len() != 0 {
        fail BadEncoding
    }
    if alg.kind == 0 {
        return PrivateKey.Ec(secret: try KeyEc(&r.value, alg.curve))
    }
    // Ed25519's CurvePrivateKey is an OCTET STRING of the 32-byte seed.
    if !KeyIs(&r.value, 4) || r.value.content.Len() != 32 {
        fail BadEncoding
    }
    var seed: [32]u8 = [0; 32]
    (mut seed[..]).CopyFrom(r.value.content)
    return PrivateKey.Ed25519(seed: seed)
}

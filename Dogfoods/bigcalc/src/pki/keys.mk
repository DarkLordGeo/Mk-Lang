// Why: keys arrive in many encodings, and every command needs them as one
// type.
//
// What: Key, Load, FromDer, CertKey, Describe and PublicPem.
//
// How: tries SubjectPublicKeyInfo, PKCS#1, PKCS#8 and SEC 1 in turn on PEM
// or DER input, or takes the key out of a certificate.

import std.big
import std.crypto
import std.encoding
import std.x509
import src.value

/// The file holds no block bigcalc reads as a key or a certificate.
export error NoKey
/// A signing operation was given a public key.
export error NotPrivate

/// KeyError is every way loading a key can fail.
export type KeyError = NoKey | NotPrivate | crypto.Error | x509.Error | value.Error | DerError

/// Key is a loaded key: a public key with the DER it was read from, or a
/// private key.
export enum Key {
    Public(key: crypto.PublicKey, der: []u8)
    Private(key: crypto.PrivateKey)
}

/// Load reads the first key of path: a PUBLIC KEY, RSA PUBLIC KEY, PRIVATE
/// KEY, EC PRIVATE KEY or CERTIFICATE block, or DER of any of them. An
/// ENCRYPTED PRIVATE KEY fails with crypto's Encrypted, and an RSA PRIVATE
/// KEY (PKCS#1, which std.crypto does not read) with BadEncoding. Allocates the file
/// and the decoded DER in a.
export Load(a: &Arena into a, io: Io, path: str): KeyError!Key from a {
    blocks := try value.Blocks(a, io, path)
    for b in blocks {
        match b.label {
            "PUBLIC KEY" | "RSA PUBLIC KEY" {
                return Key.Public(key: try crypto.ParsePublicKey(b.der), der: b.der)
            }
            "PRIVATE KEY" | "EC PRIVATE KEY" | "ENCRYPTED PRIVATE KEY" | "RSA PRIVATE KEY" {
                return Key.Private(key: try crypto.ParsePrivateKey(b.der))
            }
            "CERTIFICATE" {
                return try CertKey(a, b.der)
            }
            "" {
                return try FromDer(a, b.der)
            }
            _ {}
        }
    }
    fail NoKey
}

// A DER file: a private key, else a public key, else a certificate.
FromDer(a: &Arena, der: []u8): KeyError!Key from der {
    match crypto.ParsePrivateKey(der) {
        ok(private) {
            return Key.Private(key: private)
        }
        err(e) {
            _ = e
        }
    }
    match crypto.ParsePublicKey(der) {
        ok(public) {
            return Key.Public(key: public, der: der)
        }
        err(e) {
            _ = e
        }
    }
    return try CertKey(a, der)
}

/// CertKey is the public key of the certificate der, with its
/// SubjectPublicKeyInfo's DER. The key is read from the DER the caller holds:
/// Certificate.PublicKey's result borrows the parsed certificate, which
/// would have to outlive this call.
export CertKey(a: &Arena, der: []u8): KeyError!Key from der {
    _ = try x509.Parse(a, der)
    found := try ReadCert(a, der)
    return Key.Public(key: try crypto.ParsePublicKey(found.spki), der: found.spki)
}

/// Describe prints what kind of key k is and its public half.
export Describe(a: &Arena, out: mut Writer, k: &Key): KeyError|WriteError!void {
    match k {
        .Public(key, der) {
            try Print(out, "public key: ")
            try DescribePublic(a, out, key, *der)
        }
        .Private(key) {
            try Print(out, "private key: ")
            match key {
                .Ec(secret) {
                    var point: [97]u8 = [0; 97]
                    n := try secret.Public(mut point[..])
                    hex := try encoding.HexEncode(a, point[..n])
                    try Print(out, "P-256\npoint: {hex}\n", (hex))
                }
                .Ed25519(seed) {
                    pair := crypto.Ed25519.FromSeed(seed)
                    hex := try encoding.HexEncode(a, pair[32..])
                    try Print(out, "Ed25519\npoint: {hex}\n", (hex))
                }
            }
        }
    }
}

// The kind of a public key and its point, or an RSA key's size and exponent.
DescribePublic(a: &Arena, out: mut Writer, key: &crypto.PublicKey, der: []u8): KeyError|WriteError!void {
    match key {
        .Ec(curve, point) {
            name := CurveName(*curve)
            hex := try encoding.HexEncode(a, *point)
            try Print(out, "{name}\npoint: {hex}\n", (name, hex))
        }
        .Rsa {
            _ = try DescribeRsa(a, out, der)
        }
        .Ed25519(key: point) {
            hex := try encoding.HexEncode(a, point[..])
            try Print(out, "Ed25519\npoint: {hex}\n", (hex))
        }
    }
}

/// CurveName is the name of curve as FIPS 186 writes it.
export CurveName(curve: crypto.Curve): str {
    return match curve {
        .P256 { "P-256" }
        .P384 { "P-384" }
        _ { "an unknown curve" }
    }
}

/// DescribeRsa prints an RSA key's modulus size and exponent from its DER,
/// whether or not std.crypto accepts the key, and returns whether it does.
export DescribeRsa(a: &Arena, out: mut Writer, der: []u8): KeyError|WriteError!bool {
    rsa := try RsaKey(a, der) else {
        fail NoKey
    }
    n := try big.FromBytes(a, rsa.modulus)
    e := try big.FromBytes(a, rsa.exponent)
    bits := n.BitLen()
    exponent := try e.Text(a, 10)
    try Print(out, "RSA {bits} bits, exponent {exponent}\n", (bits, exponent))
    small := e.ToI64() else {
        return false
    }
    _ = crypto.RsaPublic.New(rsa.modulus, Cast[u64](small)) else {
        return false
    }
    return true
}

// A PKCS#1 RSAPublicKey starts with an INTEGER; a SubjectPublicKeyInfo
// with the algorithm's SEQUENCE.
IsPkcs1(der: []u8): bool {
    (value: outer, ..) := encoding.DerRead(der) else {
        return false
    }
    var parts := outer.Children()
    first := parts.Next() else {
        return false
    }
    if v := first {
        return v.tag == 2
    }
    return false
}

// Object identifiers' content bytes for the public keys bigcalc writes.
const EcPublicKey: [7]u8 = [0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01]
const Prime256v1: [8]u8 = [0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07]
const Ed25519Oid: [3]u8 = [0x2b, 0x65, 0x70]

// DER tag words (BoringSSL's form, as std.encoding takes them).
const TagSequence: u32 = 0x10
const TagOid: u32 = 0x06
const TagBitString: u32 = 0x03

/// PublicPem writes k's public key as a PEM block: the DER it was read from
/// for a public key, a SubjectPublicKeyInfo built here for a private one
/// (RFC 5480 for P-256, RFC 8410 for Ed25519).
export PublicPem(a: &Arena, out: mut Writer, k: &Key): KeyError|WriteError!void {
    match k {
        .Public(der) {
            label := if IsPkcs1(*der) { "RSA PUBLIC KEY" } else { "PUBLIC KEY" }
            try encoding.FormatPem(out, label, *der)
        }
        .Private(key) {
            var point: [97]u8 = [0; 97]
            var w := encoding.DerWriter.New(a)
            try w.Begin(TagSequence)
            try w.Begin(TagSequence)
            match key {
                .Ec(secret) {
                    n := try secret.Public(mut point[1..])
                    try w.Primitive(TagOid, EcPublicKey[..])
                    try w.Primitive(TagOid, Prime256v1[..])
                    try w.End()
                    try w.Primitive(TagBitString, point[..n + 1])
                }
                .Ed25519(seed) {
                    pair := crypto.Ed25519.FromSeed(seed)
                    (mut point[1..33]).CopyFrom(pair[32..])
                    try w.Primitive(TagOid, Ed25519Oid[..])
                    try w.End()
                    try w.Primitive(TagBitString, point[..33])
                }
            }
            try w.End()
            der := try w.Bytes()
            try encoding.FormatPem(out, "PUBLIC KEY", der)
        }
    }
}

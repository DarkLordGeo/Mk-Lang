// Why: sign and verify cover several schemes behind one interface.
//
// What: Scheme, Check, Ed25519Check, Sign, Digest and DefaultHash.
//
// How: RSA PKCS#1 v1.5 and PSS, ECDSA on P-256 and P-384 in DER or fixed
// width, and Ed25519; signing with P-256 (RFC 6979 nonces) or Ed25519.

import std.crypto
import std.hash

/// Digest is the hash h of msg, copied into a.
export Digest(a: &Arena, h: crypto.HashKind, msg: []u8): OutOfMemory![]u8 from a {
    var out := List[u8].New(a)
    match h {
        .Sha384 {
            for b in hash.Sha384(msg) {
                try out.Push(b)
            }
        }
        .Sha512 {
            for b in hash.Sha512(msg) {
                try out.Push(b)
            }
        }
        _ {
            for b in hash.Sha256(msg) {
                try out.Push(b)
            }
        }
    }
    bytes := out.Done()
    return bytes[..]
}

/// DefaultHash is the hash a key signs with when none is named: SHA-384 for
/// P-384, SHA-256 otherwise.
export DefaultHash(k: &Key): crypto.HashKind {
    match k {
        .Public(key) {
            match key {
                .Ec(curve) {
                    if *curve == crypto.Curve.P384 {
                        return crypto.HashKind.Sha384
                    }
                }
                _ {}
            }
        }
        _ {}
    }
    return crypto.HashKind.Sha256
}

/// How a signature is checked: the hash, PSS rather than PKCS#1 v1.5 for an
/// RSA key, and the encoding of an ECDSA signature.
export struct Scheme {
    export h: crypto.HashKind
    export pss: bool
    export format: crypto.SigFormat
}

/// Check reports whether sig is a valid signature of msg under k. A private
/// key checks with its public half. Variable time: every input is public.
export Check(a: &Arena, k: &Key, scheme: Scheme, msg: []u8, sig: []u8): OutOfMemory!bool {
    digest := try Digest(a, scheme.h, msg)
    match k {
        .Public(key) {
            match key {
                .Ec(curve, point) {
                    return crypto.EcdsaVerify(*point, *curve, digest, scheme.format, sig)
                }
                .Rsa(key: rsa) {
                    if scheme.pss {
                        return crypto.RsaVerifyPss(rsa, scheme.h, digest, sig)
                    }
                    return crypto.RsaVerifyPkcs1(rsa, scheme.h, digest, sig)
                }
                .Ed25519(key: point) {
                    return Ed25519Check(point, msg, sig)
                }
            }
        }
        .Private(key) {
            match key {
                .Ec(secret) {
                    var point: [65]u8 = [0; 65]
                    n := secret.Public(mut point[..]) else {
                        return false
                    }
                    return crypto.EcdsaVerify(point[..n], crypto.Curve.P256, digest, scheme.format, sig)
                }
                .Ed25519(seed) {
                    pair := crypto.Ed25519.FromSeed(seed)
                    var point: [32]u8 = [0; 32]
                    (mut point[..]).CopyFrom(pair[32..])
                    return Ed25519Check(&point, msg, sig)
                }
            }
        }
    }
}

// Ed25519 takes a signature of exactly 64 bytes.
Ed25519Check(point: &[32]u8, msg: []u8, sig: []u8): bool {
    if sig.Len() != 64 {
        return false
    }
    var fixed: [64]u8 = [0; 64]
    (mut fixed[..]).CopyFrom(sig)
    return crypto.Ed25519.Verify(point, msg, &fixed)
}

/// Sign writes the signature of msg under the private key k to the front of
/// out and returns its length: ECDSA over the digest under h with RFC 6979's
/// nonce for P-256, Ed25519 over msg itself. Fails with NotPrivate for a
/// public key, and crypto's BadLength when out is too short.
export Sign(a: &Arena, k: &Key, h: crypto.HashKind, format: crypto.SigFormat, msg: []u8, out: mut []u8): KeyError!int {
    match k {
        .Public {
            fail NotPrivate
        }
        .Private(key) {
            match key {
                .Ec(secret) {
                    digest := try Digest(a, h, msg)
                    return try crypto.EcdsaSign(secret, digest, format, out)
                }
                .Ed25519(seed) {
                    var pair := crypto.Ed25519.FromSeed(seed)
                    defer crypto.Wipe(mut pair[..])
                    sig := crypto.Ed25519.Sign(&pair, msg)
                    if out.Len() < 64 {
                        fail crypto.BadLength
                    }
                    (mut out[..64]).CopyFrom(sig[..])
                    return 64
                }
            }
        }
    }
}

/// Wipe clears the secret of a private key.
export Wipe(k: mut Key) {
    match mut k {
        .Private(key) {
            match mut key {
                .Ec(secret) {
                    secret.Wipe()
                }
                .Ed25519(seed) {
                    crypto.Wipe(mut seed[..])
                }
            }
        }
        _ {}
    }
}

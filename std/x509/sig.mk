// Why: each link of a path is a signature over the child's TBSCertificate
// by the issuer's key (RFC 5280 6.1.3 (a)(1)).
//
// What: CheckSig, Digest and Certificate.PublicKey.
//
// How: the issuer's SubjectPublicKeyInfo is parsed by crypto.ParsePublicKey,
// the TBSCertificate hashed by std.hash, and the algorithm matched to the
// key's kind. A SHA-1 or MD5 signature is refused before any arithmetic.

import std.crypto
import std.hash

// Digest is data hashed by h, in the first n bytes of sum.
Digest(h: crypto.HashKind, data: []u8): (sum: [64]u8, n: int) {
    var sum: [64]u8 = [0; 64]
    match h {
        .Sha384 {
            d := hash.Sha384(data)
            (mut sum[0..48]).CopyFrom(d[..])
            return (sum, n: 48)
        }
        .Sha512 {
            d := hash.Sha512(data)
            (mut sum[0..64]).CopyFrom(d[..])
            return (sum, n: 64)
        }
        _ {
            d := hash.Sha256(data)
            (mut sum[0..32]).CopyFrom(d[..])
            return (sum, n: 32)
        }
    }
}

// CheckSig checks that issuer's key made child's signature: Unsupported
// for a SHA-1, MD5 or unknown algorithm or a key std.crypto does not take,
// BadSignature for a signature that does not verify or an algorithm of
// another kind than the key. O(child's TBS) for the hash plus one
// verification; allocates nothing.
CheckSig(child: &Certificate, issuer: &Certificate): Unsupported|BadSignature!void {
    code := child.sig_alg
    if code == AlgWeak || code == AlgUnknown {
        fail Unsupported
    }
    key := crypto.ParsePublicKey(issuer.Spki()) else {
        fail Unsupported
    }
    var ok := false
    match key {
        .Rsa(key: rsa) {
            if code >= AlgRsaSha256 && code <= AlgPssSha512 {
                h := AlgHash(code)
                d := Digest(h, child.Tbs())
                ok = if code <= AlgRsaSha512 { crypto.RsaVerifyPkcs1(&rsa, h, d.sum[..d.n], child.Signature()) } else { crypto.RsaVerifyPss(&rsa, h, d.sum[..d.n], child.Signature()) }
            }
        }
        .Ec(curve, point) {
            if code >= AlgEcdsaSha256 && code <= AlgEcdsaSha512 {
                d := Digest(AlgHash(code), child.Tbs())
                ok = crypto.EcdsaVerify(point, curve, d.sum[..d.n], crypto.SigFormat.Der, child.Signature())
            }
        }
        .Ed25519(key: ed) {
            if code == AlgEd25519 && child.Signature().Len() == 64 {
                var s: [64]u8 = [0; 64]
                (mut s[..]).CopyFrom(child.Signature())
                ok = crypto.Ed25519.Verify(&ed, child.Tbs(), &s)
            }
        }
    }
    if !ok {
        fail BadSignature
    }
}

/// The subject's public key, read from the SubjectPublicKeyInfo by
/// crypto.ParsePublicKey: an EC point (P-256 or P-384), an RSA key or an
/// Ed25519 key, as views of the certificate's DER. Fails with
/// crypto.Error's BadEncoding for a malformed key, and BadKey for an
/// algorithm, curve or size std.crypto does not take, RSA below 2048 bits
/// included. O(key) plus one point check; allocates nothing.
export Certificate.PublicKey(&self): crypto.Error!crypto.PublicKey from self {
    return try crypto.ParsePublicKey(self.Spki())
}

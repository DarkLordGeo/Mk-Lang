// Why: Ed25519 is the common modern signature: small keys, deterministic
// signing and no nonce to get wrong.
//
// What: Ed25519 with FromSeed, Keypair, Sign and Verify, and EdClamp.
//
// How: RFC 8032 section 5.1 (pure, no context), over std.hash's SHA-512 and
// edwards.mk's group. A secret is seed || public (RFC 8032 5.1.5's 32-byte
// private key, then the encoded public key A), as Go's PrivateKey and
// libsodium's secret key. Verify is Go's crypto/ed25519.Verify: S must be
// below L, A is decoded as Go decodes it, and the check is cofactorless,
// R' = [S]B - [k]A compared bytewise with R. Constant time: Sign's
// secret-dependent work is SHA-512, the comb multiply, ScReduce64 and
// ScMulAdd, all constant time; Verify handles public data only.

import std.hash

/// Ed25519 is the Ed25519 signature scheme of RFC 8032 (pure, no context).
/// A secret key is 64 bytes, the 32-byte seed then the 32-byte public key;
/// a public key is 32 bytes and a signature 64. Call FromSeed, Keypair,
/// Sign and Verify on the type.
export struct Ed25519 {}

// EdClamp is RFC 8032 5.1.5 step 2 on the first half of h: bits 0, 1, 2
// and 255 cleared, bit 254 set.
EdClamp(h: &[64]u8): [32]u8 {
    var s: [32]u8 = [0; 32]
    (mut s[..]).CopyFrom(h[0..32])
    s[0] = s[0] & 248
    s[31] = (s[31] & 127) | 64
    return s
}

/// Returns the secret key of a 32-byte seed: the seed, then the public key
/// A, the encoding of [s]B with s the clamped first half of SHA-512(seed)
/// (RFC 8032 5.1.5). The same seed always gives the same key, so storing
/// the seed stores the key. Constant time in the seed: one SHA-512 and one
/// fixed-base multiply; allocates nothing.
export Ed25519.FromSeed(seed: &[32]u8): [64]u8 {
    h := hash.Sha512(seed[..])
    s := EdClamp(&h)
    p := PtBaseMult(&s)
    a := PtBytes(&p)
    var out: [64]u8 = [0; 64]
    (mut out[0..32]).CopyFrom(seed[..])
    (mut out[32..64]).CopyFrom(a[..])
    return out
}

/// Returns a new secret key, seed then public key, from 32 bytes of Random,
/// through FromSeed. The public key is secret[32..64]. Fails with Canceled
/// as Io.Random does. FromSeed's cost plus the system call; allocates
/// nothing.
export Ed25519.Keypair(io: Io): Canceled![64]u8 {
    var seed: [32]u8 = [0; 32]
    try Random(io, mut seed[..])
    return Ed25519.FromSeed(&seed)
}

/// Returns the signature R || S of msg under secret (RFC 8032 5.1.6).
/// Signing is deterministic: the same key and message give the same
/// signature, and no random source is used.
///
/// The public key is read from secret[32..64] and not checked. Signing with
/// a public half that does not belong to the seed leaks the secret scalar,
/// which lets anyone sign with the key, so secret must come from Keypair or
/// FromSeed, or be stored from one. Constant time in the secret: two SHA-512
/// passes over msg and one fixed-base multiply; allocates nothing.
export Ed25519.Sign(secret: &[64]u8, msg: []u8): [64]u8 {
    h := hash.Sha512(secret[0..32])
    s := EdClamp(&h)
    var rs := hash.Sha512State.New()
    rs.Bytes(h[32..64])
    rs.Bytes(msg)
    rh := rs.Sum()
    r := ScReduce64(&rh)
    rp := PtBaseMult(&r)
    re := PtBytes(&rp)
    var ks := hash.Sha512State.New()
    ks.Bytes(re[..])
    ks.Bytes(secret[32..64])
    ks.Bytes(msg)
    kh := ks.Sum()
    k := ScReduce64(&kh)
    sv := ScMulAdd(&k, &s, &r)
    var sig: [64]u8 = [0; 64]
    (mut sig[0..32]).CopyFrom(re[..])
    (mut sig[32..64]).CopyFrom(sv[..])
    return sig
}

/// Reports whether sig is a valid signature of msg under public (RFC 8032
/// 5.1.7). It refuses an S at or above the group order, accepts a public
/// key whose y is not reduced below p, and checks without the cofactor:
/// [S]B - [k]A must encode to R byte for byte. A public key that is not a
/// point is false. Variable time, since every input is public: one SHA-512
/// pass over msg and one double-scalar multiply; allocates nothing.
export Ed25519.Verify(public: &[32]u8, msg: []u8, sig: &[64]u8): bool {
    if sig[63] & 224 != 0 {
        return false
    }
    var sv: [32]u8 = [0; 32]
    (mut sv[..]).CopyFrom(sig[32..64])
    if !ScCanonical(&sv) {
        return false
    }
    pa := PtFromBytes(public) else {
        return false
    }
    var ks := hash.Sha512State.New()
    ks.Bytes(sig[0..32])
    ks.Bytes(public[..])
    ks.Bytes(msg)
    kh := ks.Sum()
    k := ScReduce64(&kh)
    na := PtNeg(&pa)
    rp := PtDoubleMult(&k, &na, &sv)
    re := PtBytes(&rp)
    return Equal(re[..], sig[0..32])
}

// Why: `enc seal` and `enc open` encrypt a file so that any change to it is
// detected.
//
// What: Seal, Open, Source (where the key comes from), Cipher and the
// Truncated and BadKeySize errors.
//
// How: AES-GCM (SP 800-38D) under a key derived with HKDF-SHA256 (RFC 5869)
// from a secret, a pseudorandom key or a P-256 ECDH shared secret. A sealed
// file is the 12-byte nonce, then the ciphertext and its 16-byte tag.

import std.crypto

/// The sealed input is shorter than a nonce and a tag.
export error Truncated
/// The key size is not 128 or 256 bits.
export error BadKeySize

/// Error is every way sealing or opening can fail.
export type Error = Truncated | BadKeySize | crypto.Error | OutOfMemory

// The bytes of a nonce and of a tag.
const NonceLen: int = 12
const TagLen: int = 16

/// Where the key comes from: a secret to extract from with a salt, or a PRK
/// already extracted.
export enum Source {
    Secret(ikm: []u8, salt: []u8)
    Prk(prk: []u8)
}

/// Cipher makes the AES-GCM key object of bits bits (128 or 256) from
/// source and info. The derived key bytes are wiped before it returns.
export Cipher(source: Source, info: []u8, bits: int): Error!crypto.AesGcm {
    if bits != 128 && bits != 256 {
        fail BadKeySize
    }
    var prk := match source {
        .Secret(ikm, salt) { crypto.HkdfExtract(salt, ikm, crypto.HashKind.Sha256) }
        .Prk(prk) { try crypto.Prk.FromBytes(crypto.HashKind.Sha256, prk) }
    }
    defer prk.Wipe()
    var key: [32]u8 = [0; 32]
    defer crypto.Wipe(mut key[..])
    try crypto.HkdfExpand(&prk, info, mut key[..bits / 8])
    return try crypto.AesGcm.New(key[..bits / 8])
}

/// Seal encrypts plain under nonce with aad, into a: the nonce, the
/// ciphertext and the tag. O(plain); allocates plain.Len() + 28 bytes.
export Seal(a: &Arena, g: &crypto.AesGcm, nonce: &[12]u8, aad: []u8, plain: []u8): Error![]u8 from a {
    var out := try a.Slice[u8](NonceLen + plain.Len() + TagLen)
    (mut out[..NonceLen]).CopyFrom(nonce[..])
    n := try g.Seal(nonce, aad, plain, mut out[NonceLen..])
    return out[..NonceLen + n]
}

/// Open checks and decrypts a sealed input, into a. Fails with Truncated
/// for an input shorter than 28 bytes and with crypto's BadTag when the
/// tag does not match (a wrong key, aad or a changed byte), writing nothing.
export Open(a: &Arena, g: &crypto.AesGcm, aad: []u8, sealed: []u8): Error![]u8 from a {
    if sealed.Len() < NonceLen + TagLen {
        fail Truncated
    }
    var nonce: [12]u8 = [0; 12]
    (mut nonce[..]).CopyFrom(sealed[..NonceLen])
    var out := try a.Slice[u8](sealed.Len() - NonceLen - TagLen)
    n := try g.Open(&nonce, aad, sealed[NonceLen..], mut out[..])
    return out[..n]
}

// Why: an encrypted message must also be authenticated, or an attacker can
// change it unseen; ChaCha20-Poly1305 is the AEAD that needs no AES
// hardware to be fast and constant time.
//
// What: ChaCha20Poly1305 with Seal and Open, and the helpers AeadLenOk and
// AeadTag.
//
// How: RFC 8439 section 2.8, the one-time Poly1305 key from block 0 (2.6),
// the message from block 1. Seal encrypts then
// authenticates; Open authenticates first and decrypts only a message whose
// tag matches, so a forged message never reaches out. Constant time: the
// tag comparison is Equal (crypto.mk), and everything else is ChaCha20 and
// Poly1305; what branches is the lengths (public).

/// ChaCha20Poly1305 is the ChaCha20-Poly1305 AEAD of RFC 8439: a 32-byte
/// key, a 12-byte nonce and a 16-byte tag. It holds no state: call Seal and
/// Open on the type. It needs no AES instructions and is constant time on
/// x86_64 and aarch64.
export struct ChaCha20Poly1305 {}

// AeadMax is the longest message: the 32-bit block counter starts at 1, so
// 2^32 - 1 blocks of 64 bytes (RFC 8439 2.8), 2^38 - 64 bytes. One block
// more would wrap ChaXor's counter to 0 and reuse block 0, the Poly1305 key.
const AeadMax: int = 274_877_906_880

// AeadLenOk reports whether a message of n bytes fits the block counter:
// n at most AeadMax. Seal and Open both ask it, and the tests pin its edge
// (TestSTDCRYPTO_AeadLengthLimit).
AeadLenOk(n: int): bool {
    return n <= AeadMax
}

// AeadTag is the tag of ciphertext ct with associated data aad: Poly1305
// over aad || pad16 || ct || pad16 || le64(aad.Len()) || le64(ct.Len()).
AeadTag(key: &ChaKey, aad: []u8, ct: []u8): [16]u8 {
    blk := ChaBlock(key, 0)
    var otk: [32]u8 = [0; 32]
    for w in 0..8 {
        PutLe32(mut otk[..], 4 * w, blk[w])
    }
    var p := PolyNew(otk[..])
    PolyPadded(mut p, aad)
    PolyPadded(mut p, ct)
    var lens: [16]u8 = [0; 16]
    PutLe64(mut lens[..], 0, Trunc[u64](aad.Len()))
    PutLe64(mut lens[..], 8, Trunc[u64](ct.Len()))
    PolyBlocks(mut p, lens[..], 1)
    return PolySum(&p)
}

/// Encrypts plain under key and nonce and writes the ciphertext, then a
/// 16-byte tag over aad and the ciphertext, to out. out must be exactly
/// plain.Len() + 16 bytes and plain at most 274,877,906,880 bytes;
/// otherwise it fails with BadLength and writes nothing. Never use a nonce
/// twice under one key: Seal cannot detect it, and a repeat reveals the XOR
/// of the two plaintexts and lets an attacker forge messages. O(plain +
/// aad); allocates nothing.
export ChaCha20Poly1305.Seal(key: &[32]u8, nonce: &[12]u8, aad: []u8, plain: []u8, out: mut []u8): Error!void {
    n := plain.Len()
    if !AeadLenOk(n) || out.Len() != n + 16 {
        fail BadLength
    }
    k := ChaKeyOf(key, nonce)
    ChaXor(&k, 1, plain, mut out[0..n])
    tag := AeadTag(&k, aad, out[0..n])
    (mut out[n..n + 16]).CopyFrom(tag[..])
}

/// Checks the tag at the end of sealed over aad and the ciphertext, then
/// writes the plaintext to out. out must be exactly sealed.Len() - 16
/// bytes, and sealed at least 16; otherwise BadLength. A tag that does not
/// match is BadTag, found before any byte of out is written, so out is
/// unchanged. The tag comparison is constant time. O(sealed + aad);
/// allocates nothing.
export ChaCha20Poly1305.Open(key: &[32]u8, nonce: &[12]u8, aad: []u8, sealed: []u8, out: mut []u8): Error!void {
    if sealed.Len() < 16 {
        fail BadLength
    }
    n := sealed.Len() - 16
    if !AeadLenOk(n) || out.Len() != n {
        fail BadLength
    }
    k := ChaKeyOf(key, nonce)
    tag := AeadTag(&k, aad, sealed[0..n])
    if !Equal(tag[..], sealed[n..n + 16]) {
        fail BadTag
    }
    ChaXor(&k, 1, sealed[0..n], out)
}

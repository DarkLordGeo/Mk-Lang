// Why: ChaCha20 is the stream cipher of ChaCha20-Poly1305, fast and
// constant time in plain integer code.
//
// What: ChaKey, ChaBlock and the keystream XOR ChaXor, with its four-block
// vector form ChaXor4.
//
// How: the ChaCha20 block function and keystream of RFC 8439 sections 2.1 to
// 2.4 (Bernstein's ChaCha with the IETF 96-bit nonce and 32-bit counter).
// Constant time by construction: 32-bit add, rotate by constants and xor only,
// a fixed 10 double rounds, and no index that depends on the key. ChaXor runs
// four blocks at once while 256 bytes remain (ChaXor4: the 16 state words as 16
// vectors of 4 lanes, lane j holding block ctr + j, as Go's amd64 assembly,
// BoringSSL's chacha_vec and Zig's ChaChaVecImpl do; the 16-lane baseline,
// since std.crypto is not an unsafe module and takes no avx2 dispatch), then
// one block at a time on the 16 state words in locals. Both paths compute the
// same words.

import std.simd

// ChaKey is a key and nonce as little-endian words (RFC 8439 2.3).
struct ChaKey {
    k: [8]u32
    n: [3]u32
}

// ChaKeyOf reads the 32-byte key and the 12-byte nonce.
ChaKeyOf(key: &[32]u8, nonce: &[12]u8): ChaKey {
    var k: [8]u32 = [0; 8]
    for i in 0..8 {
        k[i] = Le32(key[..], 4 * i)
    }
    return ChaKey(k: k, n: [Le32(nonce[..], 0), Le32(nonce[..], 4), Le32(nonce[..], 8)])
}

RotL32(x: u32, n: int): u32 {
    return (x << n) | (x >> (32 - n))
}

// Qr is the quarter round of RFC 8439 2.1 on four state words.
Qr(a: mut u32, b: mut u32, c: mut u32, d: mut u32) {
    *a = *a +% *b
    *d = RotL32(*d ^ *a, 16)
    *c = *c +% *d
    *b = RotL32(*b ^ *c, 12)
    *a = *a +% *b
    *d = RotL32(*d ^ *a, 8)
    *c = *c +% *d
    *b = RotL32(*b ^ *c, 7)
}

// ChaBlock is the 64-byte block for counter ctr as 16 words (RFC 8439
// 2.3): 20 rounds as 10 column-then-diagonal double rounds, then the input
// state added word by word.
ChaBlock(key: &ChaKey, ctr: u32): [16]u32 {
    var x0: u32 = 0x61707865
    var x1: u32 = 0x3320646e
    var x2: u32 = 0x79622d32
    var x3: u32 = 0x6b206574
    var x4 := key.k[0]
    var x5 := key.k[1]
    var x6 := key.k[2]
    var x7 := key.k[3]
    var x8 := key.k[4]
    var x9 := key.k[5]
    var x10 := key.k[6]
    var x11 := key.k[7]
    var x12 := ctr
    var x13 := key.n[0]
    var x14 := key.n[1]
    var x15 := key.n[2]
    for _ in 0..10 {
        Qr(mut x0, mut x4, mut x8, mut x12)
        Qr(mut x1, mut x5, mut x9, mut x13)
        Qr(mut x2, mut x6, mut x10, mut x14)
        Qr(mut x3, mut x7, mut x11, mut x15)
        Qr(mut x0, mut x5, mut x10, mut x15)
        Qr(mut x1, mut x6, mut x11, mut x12)
        Qr(mut x2, mut x7, mut x8, mut x13)
        Qr(mut x3, mut x4, mut x9, mut x14)
    }
    return [x0 +% 0x61707865, x1 +% 0x3320646e, x2 +% 0x79622d32, x3 +% 0x6b206574,
        x4 +% key.k[0], x5 +% key.k[1], x6 +% key.k[2], x7 +% key.k[3],
        x8 +% key.k[4], x9 +% key.k[5], x10 +% key.k[6], x11 +% key.k[7],
        x12 +% ctr, x13 +% key.n[0], x14 +% key.n[1], x15 +% key.n[2]]
}

// ChaXor writes src XOR the keystream from block ctr on into dst, which
// has src's length (RFC 8439 2.4): whole blocks as 16 word loads and
// stores, the tail through one keystream block on the stack. The caller
// keeps ctr + blocks within 2^32 (aead.mk's limit).
ChaXor(key: &ChaKey, ctr: u32, src: []u8, dst: mut []u8) {
    n := src.Len()
    var i := 0
    var c := ctr
    for n - i >= 256 {
        ChaXor4(key, c, src[i..i + 256], mut dst[i..i + 256])
        c = c +% 4
        i += 256
    }
    for n - i >= 64 {
        ks := ChaBlock(key, c)
        s := src[i..n][0..64]
        t := mut dst[i..n]
        d := mut t[0..64]
        for w in 0..16 {
            PutLe32(mut d[..], 4 * w, Le32(s, 4 * w) ^ ks[w])
        }
        c = c +% 1
        i += 64
    }
    if i < n {
        ks := ChaBlock(key, c)
        var b: [64]u8 = [0; 64]
        for w in 0..16 {
            PutLe32(mut b[..], 4 * w, ks[w])
        }
        for j in i..n {
            dst[j] = src[j] ^ b[j - i]
        }
    }
}

// RotV rotates each lane of x left by n.
RotV(x: simd.Vec[u32, 4], const n: int): simd.Vec[u32, 4] {
    return (x << n) | (x >> (32 - n))
}

// QrV is Qr on four blocks at once, one lane each.
QrV(a: mut simd.Vec[u32, 4], b: mut simd.Vec[u32, 4], c: mut simd.Vec[u32, 4], d: mut simd.Vec[u32, 4]) {
    *a = *a +% *b
    *d = RotV(*d ^ *a, 16)
    *c = *c +% *d
    *b = RotV(*b ^ *c, 12)
    *a = *a +% *b
    *d = RotV(*d ^ *a, 8)
    *c = *c +% *d
    *b = RotV(*b ^ *c, 7)
}

// ChaXor4 writes src XOR the keystream of blocks ctr to ctr + 3 into dst,
// both 256 bytes. Word w of block ctr + j is lane j of xw; after the
// rounds and the input added, each group of four words is transposed
// (two interleaves and two shuffles), so block j's words 4q to 4q + 3 are
// one vector, XORed with 16 bytes of src as one little-endian vector.
ChaXor4(key: &ChaKey, ctr: u32, src: []u8, dst: mut []u8) {
    var x: [16]simd.Vec[u32, 4] = [simd.Vec[u32, 4].Zero(); 16]
    x[0] = simd.Vec[u32, 4].Splat(0x61707865)
    x[1] = simd.Vec[u32, 4].Splat(0x3320646e)
    x[2] = simd.Vec[u32, 4].Splat(0x79622d32)
    x[3] = simd.Vec[u32, 4].Splat(0x6b206574)
    for w in 0..8 {
        x[4 + w] = simd.Vec[u32, 4].Splat(key.k[w])
    }
    x[12] = simd.Vec[u32, 4].Splat(ctr) +% simd.Vec[u32, 4].Iota()
    x[13] = simd.Vec[u32, 4].Splat(key.n[0])
    x[14] = simd.Vec[u32, 4].Splat(key.n[1])
    x[15] = simd.Vec[u32, 4].Splat(key.n[2])
    var y0 := x[0]
    var y1 := x[1]
    var y2 := x[2]
    var y3 := x[3]
    var y4 := x[4]
    var y5 := x[5]
    var y6 := x[6]
    var y7 := x[7]
    var y8 := x[8]
    var y9 := x[9]
    var y10 := x[10]
    var y11 := x[11]
    var y12 := x[12]
    var y13 := x[13]
    var y14 := x[14]
    var y15 := x[15]
    for _ in 0..10 {
        QrV(mut y0, mut y4, mut y8, mut y12)
        QrV(mut y1, mut y5, mut y9, mut y13)
        QrV(mut y2, mut y6, mut y10, mut y14)
        QrV(mut y3, mut y7, mut y11, mut y15)
        QrV(mut y0, mut y5, mut y10, mut y15)
        QrV(mut y1, mut y6, mut y11, mut y12)
        QrV(mut y2, mut y7, mut y8, mut y13)
        QrV(mut y3, mut y4, mut y9, mut y14)
    }
    ChaOut4(y0 +% x[0], y1 +% x[1], y2 +% x[2], y3 +% x[3], src, dst, 0)
    ChaOut4(y4 +% x[4], y5 +% x[5], y6 +% x[6], y7 +% x[7], src, dst, 16)
    ChaOut4(y8 +% x[8], y9 +% x[9], y10 +% x[10], y11 +% x[11], src, dst, 32)
    ChaOut4(y12 +% x[12], y13 +% x[13], y14 +% x[14], y15 +% x[15], src, dst, 48)
}

// ChaOut4 transposes four state words of four blocks (a holds word 4q of
// blocks 0 to 3, b word 4q + 1, and so on) into each block's 16 bytes at
// offset off of its 64, and XORs them with src into dst.
ChaOut4(a: simd.Vec[u32, 4], b: simd.Vec[u32, 4], c: simd.Vec[u32, 4], d: simd.Vec[u32, 4], src: []u8, dst: mut []u8, off: int) {
    t0 := a.InterleaveLo(b)
    t1 := c.InterleaveLo(d)
    t2 := a.InterleaveHi(b)
    t3 := c.InterleaveHi(d)
    ChaPut(t0.Shuffle(t1, [0, 1, 4, 5]), src, dst, off)
    ChaPut(t0.Shuffle(t1, [2, 3, 6, 7]), src, dst, 64 + off)
    ChaPut(t2.Shuffle(t3, [0, 1, 4, 5]), src, dst, 128 + off)
    ChaPut(t2.Shuffle(t3, [2, 3, 6, 7]), src, dst, 192 + off)
}

// ChaPut XORs the keystream words k, little-endian, with src[at..at + 16]
// into dst there.
ChaPut(k: simd.Vec[u32, 4], src: []u8, dst: mut []u8, at: int) {
    s := simd.Vec[u8, 16].FromSlice(src, at)
    (s ^ k.Reinterpret[u8, 16]()).CopyTo(dst, at)
}

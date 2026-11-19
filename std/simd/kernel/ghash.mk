// Why: AES-GCM's authentication hashes every byte with GHASH, and a bit-wise
// GF(2^128) multiply in software is slow; the carry-less multiply
// instruction does it in a few cycles.
//
// What: GhashKey, which computes the key's table H .. H^4, and GhashBlocks, the
// block kernel, each behind its simd.Cpu() guard: GCM's hash (NIST SP 800-38D
// sections 6.3 and 6.4), multiplication in GF(2^128) modulo
// x^128 + x^7 + x^2 + x + 1 with GCM's bit order (bit 7 of byte 0 is the
// coefficient of x^0).
//
// How: a block is held as two u64, hi the big-endian word of bytes 0 to 7
// and lo that of bytes 8 to 15, so the coefficient of x^i is bit 127 - i:
// the operands are reflected. The 255-bit carry-less product of two
// reflected operands is the reflected product shifted right by one, so it is
// shifted left once, then reduced (Gueron and Kounavis, "Intel Carry-Less
// Multiplication Instruction and its Usage for Computing the GCM Mode",
// 2014, algorithms 2 and 5: Karatsuba's three products, the shift-based
// reduction). Four blocks are aggregated per reduction with H^4 .. H
// (algorithm 3's method): ((Y ^ X1) H^4) ^ (X2 H^3) ^ (X3 H^2) ^ (X4 H).
// The powers are computed once by GhashKey and held by the caller's key
// object, so a call pays no setup (recomputing them cost nine carry-less
// products and three reductions per call). Each cpu clause names sse2 too:
// PCLMULQDQ works in xmm registers (E1106 needs on x86_64; aarch64 ignores
// the name).

import std.raw
import std.simd

// Word is one GF(2^128) element: hi holds the coefficients of x^0 to x^63.
struct Word {
    hi: u64
    lo: u64
}

// Split reads block b as a Word.
Split(b: [16]u8): Word {
    v := simd.Vec[u8, 16].FromArray(b).Reverse().Reinterpret[u64, 2]()
    return Word(hi: v.Lane(1), lo: v.Lane(0))
}

// Join is w as a block.
Join(w: Word): [16]u8 {
    return simd.Vec[u64, 2].FromArray([w.lo, w.hi]).Reinterpret[u8, 16]().Reverse().ToArray()
}

// Product is the 256-bit carry-less product of a and b as four words,
// most significant first, by Karatsuba: three Clmul64.
Product(a: Word, b: Word): [4]u64 cpu "sse2, pclmul, aes" {
    z2 := raw.Clmul64(a.hi, b.hi)
    z0 := raw.Clmul64(a.lo, b.lo)
    z1 := raw.Clmul64(a.hi ^ a.lo, b.hi ^ b.lo)
    m1 := z1.hi ^ z2.hi ^ z0.hi
    m0 := z1.lo ^ z2.lo ^ z0.lo
    return [z2.hi, z2.lo ^ m1, z0.hi ^ m0, z0.lo]
}

// Xor4 is a ^ b wordwise.
Xor4(a: [4]u64, b: [4]u64): [4]u64 {
    return [a[0] ^ b[0], a[1] ^ b[1], a[2] ^ b[2], a[3] ^ b[3]]
}

// Reduce is the product p of two reflected elements as an element: p
// shifted left once, then reduced modulo the field polynomial.
Reduce(p: [4]u64): Word {
    x3 := (p[0] << 1) | (p[1] >> 63)
    x2 := (p[1] << 1) | (p[2] >> 63)
    x1 := (p[2] << 1) | (p[3] >> 63)
    x0 := p[3] << 1
    d := x1 ^ (x0 << 63) ^ (x0 << 62) ^ (x0 << 57)
    e1 := d >> 1
    e0 := (x0 >> 1) | (d << 63)
    f1 := d >> 2
    f0 := (x0 >> 2) | (d << 62)
    g1 := d >> 7
    g0 := (x0 >> 7) | (d << 57)
    return Word(hi: x3 ^ d ^ e1 ^ f1 ^ g1, lo: x2 ^ x0 ^ e0 ^ f0 ^ g0)
}

// Mul is a * b in GF(2^128).
Mul(a: Word, b: Word): Word cpu "sse2, pclmul, aes" {
    return Reduce(Product(a, b))
}

// XorW is a ^ b.
XorW(a: Word, b: Word): Word {
    return Word(hi: a.hi ^ b.hi, lo: a.lo ^ b.lo)
}

// Powers reads H, H^2, H^3 and H^4 from powers (Join's byte order each).
Powers(powers: &[64]u8): [4]Word {
    p := powers[..]
    return [Split(Load(p, 0)), Split(Load(p, 16)), Split(Load(p, 32)), Split(Load(p, 48))]
}

// KeyNi is GhashKey's body.
KeyNi(h: &[16]u8, powers: mut [64]u8) cpu "sse2, pclmul, aes" {
    h1 := Split(*h)
    h2 := Mul(h1, h1)
    h3 := Mul(h2, h1)
    h4 := Mul(h3, h1)
    w := [Join(h1), Join(h2), Join(h3), Join(h4)]
    for j in 0..4 {
        for b in 0..16 {
            powers[16 * j + b] = w[j][b]
        }
    }
}

// GhashNi is GhashBlocks' body: four blocks per reduction with the
// caller's H^4 .. H, then one block at a time with H.
GhashNi(powers: &[64]u8, y: mut [16]u8, data: []u8) cpu "sse2, pclmul, aes" {
    hp := Powers(powers)
    var acc := Split(*y)
    n := data.Len() / 16 * 16
    var i := 0
    for i + 64 <= n {
        var p := Product(XorW(acc, Split(Load(data, i))), hp[3])
        p = Xor4(p, Product(Split(Load(data, i + 16)), hp[2]))
        p = Xor4(p, Product(Split(Load(data, i + 32)), hp[1]))
        p = Xor4(p, Product(Split(Load(data, i + 48)), hp[0]))
        acc = Reduce(p)
        i += 64
    }
    for i < n {
        acc = Mul(XorW(acc, Split(Load(data, i))), hp[0])
        i += 16
    }
    *y = Join(acc)
}

/// GhashKey writes the GHASH table of h = H = E(K, 0^128) to powers: H,
/// H^2, H^3 and H^4 in GCM's GF(2^128) (NIST SP 800-38D section 6.3), 16
/// bytes each in the order of a block, and reports true; it reports false,
/// writing nothing, when the CPU lacks pclmul and aes. A key object computes
/// it once and passes it to every GhashBlocks call; it is key material, so
/// the object wipes it (Wipe) when it dies. O(1): nine carry-less products
/// and three reductions; allocates nothing.
export GhashKey(h: &[16]u8, powers: mut [64]u8): bool {
    if Target.os == Os.Freestanding {
        return false
    }
    if !simd.Cpu().Has("pclmul") || !simd.Cpu().Has("aes") {
        return false
    }
    unsafe "Cpu() reports pclmul and aes, which KeyNi's clause names" {
        KeyNi(h, powers)
    }
    return true
}

/// GhashBlocks updates y over every whole 16-byte block X of data, in
/// order, to (y ^ X) * H in GCM's GF(2^128) (NIST SP 800-38D section 6.4),
/// powers being GhashKey's table for H, and reports true; it reports false,
/// writing nothing, when the CPU lacks pclmul and aes. data.Len() % 16
/// trailing bytes are ignored. O(n), four blocks per reduction with no
/// per-call setup; allocates nothing.
export GhashBlocks(powers: &[64]u8, y: mut [16]u8, data: []u8): bool {
    if Target.os == Os.Freestanding {
        return false
    }
    if !simd.Cpu().Has("pclmul") || !simd.Cpu().Has("aes") {
        return false
    }
    unsafe "Cpu() reports pclmul and aes, which GhashNi's clause names" {
        GhashNi(powers, y, data)
    }
    return true
}

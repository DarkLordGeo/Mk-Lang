// Why: SHA-512 (and SHA-384) has no dedicated instruction on common CPUs,
// but AVX2 and BMI2 still run it markedly faster than portable code.
//
// What: Sha512Blocks, the SHA-512 kernel (FIPS 180-4 section 6.4) behind its
// simd.Cpu() guard, and the round constants K512.
//
// How: Sha512Blocks returns false, having written nothing, when the running
// CPU lacks avx2 or bmi2 (always on aarch64, on x86_64-freestanding and in
// the evaluator, where Cpu() is the baseline); the caller then runs its
// portable loop, which is the reference. The shape is Intel's AVX2 SHA-512
// (Guilford, Yap and Gopal, "Fast SHA-512 Implementations on Intel
// Architecture Processors", 2012; Go's sha512block_amd64 and OpenSSL's
// sha512-x86_64 AVX2 path): the message schedule four words at a time in
// 256-bit lanes, W + K added lanewise, beside scalar rounds, which bmi2
// compiles to rorx (a rotate that keeps its source and sets no flags). The
// schedule's four words W[t] .. W[t+3] need sigma1 of W[t-2] .. W[t+1], and
// the last two of those are the first two of the four, so each step
// computes lanes 0 and 1, then lanes 2 and 3 from them.

import std.simd

// K512 are FIPS 180-4 section 4.2.3's constants, four per round group.
const K512: [20][4]u64 = [
    [0x428a2f98d728ae22, 0x7137449123ef65cd, 0xb5c0fbcfec4d3b2f, 0xe9b5dba58189dbbc],
    [0x3956c25bf348b538, 0x59f111f1b605d019, 0x923f82a4af194f9b, 0xab1c5ed5da6d8118],
    [0xd807aa98a3030242, 0x12835b0145706fbe, 0x243185be4ee4b28c, 0x550c7dc3d5ffb4e2],
    [0x72be5d74f27b896f, 0x80deb1fe3b1696b1, 0x9bdc06a725c71235, 0xc19bf174cf692694],
    [0xe49b69c19ef14ad2, 0xefbe4786384f25e3, 0x0fc19dc68b8cd5b5, 0x240ca1cc77ac9c65],
    [0x2de92c6f592b0275, 0x4a7484aa6ea6e483, 0x5cb0a9dcbd41fbd4, 0x76f988da831153b5],
    [0x983e5152ee66dfab, 0xa831c66d2db43210, 0xb00327c898fb213f, 0xbf597fc7beef0ee4],
    [0xc6e00bf33da88fc2, 0xd5a79147930aa725, 0x06ca6351e003826f, 0x142929670a0e6e70],
    [0x27b70a8546d22ffc, 0x2e1b21385c26c926, 0x4d2c6dfc5ac42aed, 0x53380d139d95b3df],
    [0x650a73548baf63de, 0x766a0abb3c77b2a8, 0x81c2c92e47edaee6, 0x92722c851482353b],
    [0xa2bfe8a14cf10364, 0xa81a664bbc423001, 0xc24b8b70d0f89791, 0xc76c51a30654be30],
    [0xd192e819d6ef5218, 0xd69906245565a910, 0xf40e35855771202a, 0x106aa07032bbd1b8],
    [0x19a4c116b8d2d0c8, 0x1e376c085141ab53, 0x2748774cdf8eeb99, 0x34b0bcb5e19b48a8],
    [0x391c0cb3c5c95a63, 0x4ed8aa4ae3418acb, 0x5b9cca4f7763e373, 0x682e6ff3d6b2b8a3],
    [0x748f82ee5defb2fc, 0x78a5636f43172f60, 0x84c87814a1f0ab72, 0x8cc702081a6439ec],
    [0x90befffa23631e28, 0xa4506cebde82bde9, 0xbef9a3f7b2c67915, 0xc67178f2e372532b],
    [0xca273eceea26619c, 0xd186b8c721c0c207, 0xeada7dd6cde0eb1e, 0xf57d4f7fee6ed178],
    [0x06f067aa72176fba, 0x0a637dc5a2c898a6, 0x113f9804bef90dae, 0x1b710b35131c471b],
    [0x28db77f523047d84, 0x32caab7b40c72493, 0x3c9ebe0a15c9bebc, 0x431d67c49c100d4c],
    [0x4cc5d4becb3e42b6, 0x597f299cfc657e2a, 0x5fcb6fab3ad6faec, 0x6c44198c4a475817],
]

// BeWords4 is the four big-endian words of data from i.
BeWords4(data: []u8, i: int): simd.Vec[u64, 4] {
    v := simd.Vec[u8, 32].FromSlice(data, i)
    return v.Shuffle(v, [7, 6, 5, 4, 3, 2, 1, 0, 15, 14, 13, 12, 11, 10, 9, 8, 23, 22, 21, 20, 19, 18, 17, 16, 31, 30, 29, 28, 27, 26, 25, 24]).Reinterpret[u64, 4]()
}

// Ror4 rotates each lane of x right by k, 0 < k < 64.
Ror4(x: simd.Vec[u64, 4], const k: int): simd.Vec[u64, 4] {
    return (x >> k) | (x << (64 - k))
}

// Sigma0 is FIPS 180-4's lower-case sigma0 of SHA-512, lanewise.
Sigma0(x: simd.Vec[u64, 4]): simd.Vec[u64, 4] {
    return Ror4(x, 1) ^ Ror4(x, 8) ^ (x >> 7)
}

// Sigma1 is FIPS 180-4's lower-case sigma1 of SHA-512, lanewise.
Sigma1(x: simd.Vec[u64, 4]): simd.Vec[u64, 4] {
    return Ror4(x, 19) ^ Ror4(x, 61) ^ (x >> 6)
}

// Schedule4 is W[t] .. W[t+3] from w0 = W[t-16] .. W[t-13], w1, w2 and
// w3 = W[t-4] .. W[t-1].
Schedule4(w0: simd.Vec[u64, 4], w1: simd.Vec[u64, 4], w2: simd.Vec[u64, 4], w3: simd.Vec[u64, 4]): simd.Vec[u64, 4] {
    z := simd.Vec[u64, 4].Zero()
    // W[t-15] .. W[t-12] and W[t-7] .. W[t-4].
    p := w0 +% Sigma0(w1.Slide(w0, 3)) +% w3.Slide(w2, 3)
    // Lanes 0 and 1 take sigma1 of W[t-2] and W[t-1] (w3's lanes 2 and 3).
    lo := p +% Sigma1(w3).Shuffle(z, [2, 3, 4, 4])
    // Lanes 2 and 3 take sigma1 of W[t] and W[t+1] (lo's lanes 0 and 1).
    return lo +% Sigma1(lo).Shuffle(z, [4, 4, 0, 1])
}

// Ror64 is x rotated right by k, 0 < k < 64.
Ror64(x: u64, k: int): u64 {
    return (x >> k) | (x << (64 - k))
}

// Sha512Round is one round on the working variables, written for the
// renaming schedule as std.hash's own Sha512Round: d and h are updated in
// place, and the caller rotates the names. kw is K[t] + W[t].
Sha512Round(a: u64, b: u64, c: u64, d: mut u64, e: u64, f: u64, g: u64, h: mut u64, kw: u64) {
    t1 := *h +% (Ror64(e, 14) ^ Ror64(e, 18) ^ Ror64(e, 41)) +% (g ^ (e & (f ^ g))) +% kw
    *d = *d +% t1
    *h = t1 +% (Ror64(a, 28) ^ Ror64(a, 34) ^ Ror64(a, 39)) +% ((a & b) | (c & (a | b)))
}

// Sha512Avx2 is Sha512Blocks' body.
Sha512Avx2(h: mut [8]u64, data: []u8) cpu "avx2, bmi2" {
    n := data.Len() / 128 * 128
    var i := 0
    for i < n {
        var a := h[0]
        var b := h[1]
        var c := h[2]
        var d := h[3]
        var e := h[4]
        var f := h[5]
        var g := h[6]
        var hh := h[7]
        // w0 .. w3 are the last sixteen schedule words, oldest first; each
        // step shifts them down (the last four steps need no new words),
        // so no word is indexed at run time.
        var w0 := BeWords4(data, i)
        var w1 := BeWords4(data, i + 32)
        var w2 := BeWords4(data, i + 64)
        var w3 := BeWords4(data, i + 96)
        for q in 0..10 {
            k0 := (w0 +% simd.Vec[u64, 4].FromArray(K512[2 * q])).ToArray()
            k1 := (w1 +% simd.Vec[u64, 4].FromArray(K512[2 * q + 1])).ToArray()
            Sha512Round(a, b, c, mut d, e, f, g, mut hh, k0[0])
            Sha512Round(hh, a, b, mut c, d, e, f, mut g, k0[1])
            Sha512Round(g, hh, a, mut b, c, d, e, mut f, k0[2])
            Sha512Round(f, g, hh, mut a, b, c, d, mut e, k0[3])
            Sha512Round(e, f, g, mut hh, a, b, c, mut d, k1[0])
            Sha512Round(d, e, f, mut g, hh, a, b, mut c, k1[1])
            Sha512Round(c, d, e, mut f, g, hh, a, mut b, k1[2])
            Sha512Round(b, c, d, mut e, f, g, hh, mut a, k1[3])
            w4 := if q < 8 { Schedule4(w0, w1, w2, w3) } else { w2 }
            w5 := if q < 8 { Schedule4(w1, w2, w3, w4) } else { w3 }
            w0 = w2
            w1 = w3
            w2 = w4
            w3 = w5
        }
        h[0] = h[0] +% a
        h[1] = h[1] +% b
        h[2] = h[2] +% c
        h[3] = h[3] +% d
        h[4] = h[4] +% e
        h[5] = h[5] +% f
        h[6] = h[6] +% g
        h[7] = h[7] +% hh
        i += 128
    }
}

/// Sha512Blocks compresses every whole 128-byte block of data into h, in
/// order (FIPS 180-4 section 6.4.2; h is H0 .. H7), and reports true; it
/// reports false, writing nothing, when the CPU lacks avx2 or bmi2.
/// data.Len() % 128 trailing bytes are ignored. O(n); allocates nothing.
export Sha512Blocks(h: mut [8]u64, data: []u8): bool {
    if Target.os == Os.Freestanding {
        return false
    }
    c := simd.Cpu()
    if !c.Has("avx2") || !c.Has("bmi2") {
        return false
    }
    unsafe "Cpu() reports avx2 and bmi2, which Sha512Avx2's clause names" {
        Sha512Avx2(h, data)
    }
    return true
}

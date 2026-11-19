// Why: Ed25519 reduces hashes and computes S modulo the group order L, in
// constant time for signing.
//
// What: ScReduce64, ScMulAdd and ScCanonical, over ScFold and the carry
// rounds.
//
// How: arithmetic modulo the group order L = 2^252 +
// 27742317777372353535851937790883648493 (RFC 8032 5.1), as ref10's sc_reduce
// and sc_muladd (SUPERCOP, the code libsodium and Go's pre-2021 edwards25519
// carry), written as loops: a number is 24 signed limbs of radix 2^21 in i64,
// and a limb at 2^252 or above folds down by the identity 2^252 = -c (mod L),
// c = L - 2^252, whose signed radix-2^21 digits are the six constants of
// ScFold. Constant time: the loops have fixed bounds, every carry is an
// arithmetic shift, and no branch or index depends on a limb. The bounds that
// keep every i64 exact are at each step;
// TestSTDCRYPTO_ScalarAndPointMatchModels checks the results against
// math/big on edge and seeded random inputs.

// ScFold folds limb i (at 2^(21 i), i >= 12) into limbs i - 12 to i - 7:
// s[i] 2^(21 i) = s[i] 2^(21 (i - 12)) 2^252 = -c s[i] 2^(21 (i - 12)).
ScFold(s: mut [24]i64, i: int) {
    x := s[i]
    s[i - 12] = s[i - 12] +% x *% 666643
    s[i - 11] = s[i - 11] +% x *% 470296
    s[i - 10] = s[i - 10] +% x *% 654183
    s[i - 9] = s[i - 9] -% x *% 997805
    s[i - 8] = s[i - 8] +% x *% 136657
    s[i - 7] = s[i - 7] -% x *% 683901
    s[i] = 0
}

// ScCarryRound moves limb i's bits above 21 up one limb, rounding so the
// limb ends in [-2^20, 2^20).
ScCarryRound(s: mut [24]i64, i: int) {
    c := (s[i] +% 1048576) >> 21
    s[i + 1] = s[i + 1] +% c
    s[i] = s[i] -% (c << 21)
}

// ScCarryFloor moves limb i's bits above 21 up one limb, so the limb ends
// in [0, 2^21).
ScCarryFloor(s: mut [24]i64, i: int) {
    c := s[i] >> 21
    s[i + 1] = s[i + 1] +% c
    s[i] = s[i] -% (c << 21)
}

// ScReduceLimbs is the value of s mod L, canonical, 32 bytes little-endian
// (sc_reduce's tail). s's limbs must be below 2^30 in magnitude, the top
// one below 2^34: then every product below is under 2^54 and every sum
// under 2^56.
ScReduceLimbs(s: mut [24]i64): [32]u8 {
    var i := 23
    for i >= 18 {
        ScFold(s, i)
        i -= 1
    }
    for j in 0..6 {
        ScCarryRound(s, 6 + 2 * j)
    }
    for j in 0..5 {
        ScCarryRound(s, 7 + 2 * j)
    }
    i = 17
    for i >= 12 {
        ScFold(s, i)
        i -= 1
    }
    for j in 0..6 {
        ScCarryRound(s, 2 * j)
    }
    for j in 0..6 {
        ScCarryRound(s, 2 * j + 1)
    }
    ScFold(s, 12)
    for j in 0..12 {
        ScCarryFloor(s, j)
    }
    ScFold(s, 12)
    for j in 0..11 {
        ScCarryFloor(s, j)
    }
    // Now 0 <= value < L, limbs 0 to 10 in [0, 2^21) and limb 11 the rest.
    var out: [32]u8 = [0; 32]
    var acc: u64 = 0
    var bits := 0
    var o := 0
    for j in 0..12 {
        acc = acc | (Trunc[u64](s[j]) << bits)
        bits += 21
        for bits >= 8 {
            out[o] = Trunc[u8](acc)
            acc = acc >> 8
            bits -= 8
            o += 1
        }
    }
    out[o] = Trunc[u8](acc)
    return out
}

// ScBits is the n bits of b from bit off, little-endian, n at most 32.
// Only public offsets and lengths decide what is read.
ScBits(b: []u8, off: int, n: int): i64 {
    k := off >> 3
    var v: u64 = 0
    for j in 0..5 {
        if k + j < b.Len() {
            v = v | (u64(b[k + j]) << (8 * j))
        }
    }
    return Trunc[i64]((v >> (off & 7)) & ((Trunc[u64](1) << n) - 1))
}

// ScReduce64 is the 64-byte little-endian h mod L (RFC 8032 5.1.6 step 2
// and 5.1.7 step 2's "interpret as an integer and reduce mod L").
ScReduce64(h: &[64]u8): [32]u8 {
    var s: [24]i64 = [0; 24]
    for i in 0..23 {
        s[i] = ScBits(h[..], 21 * i, 21)
    }
    s[23] = ScBits(h[..], 483, 29)
    return ScReduceLimbs(mut s)
}

// ScLimbs32 is a 32-byte little-endian number as 12 limbs, the last of 25
// bits.
ScLimbs32(b: &[32]u8): [12]i64 {
    var a: [12]i64 = [0; 12]
    for i in 0..11 {
        a[i] = ScBits(b[..], 21 * i, 21)
    }
    a[11] = ScBits(b[..], 231, 25)
    return a
}

// ScMulAdd is (a b + c) mod L for any 32-byte a, b and c (sc_muladd;
// RFC 8032 5.1.6 step 5's S = (r + k s) mod L). Each product column is a
// sum of at most 12 products below 2^50, so below 2^54, then carried
// before the reduction.
ScMulAdd(a: &[32]u8, b: &[32]u8, c: &[32]u8): [32]u8 {
    x := ScLimbs32(a)
    y := ScLimbs32(b)
    z := ScLimbs32(c)
    var s: [24]i64 = [0; 24]
    (mut s[0..12]).CopyFrom(z[..])
    for i in 0..12 {
        for j in 0..12 {
            s[i + j] = s[i + j] +% x[i] *% y[j]
        }
    }
    for j in 0..12 {
        ScCarryRound(mut s, 2 * j)
    }
    for j in 0..11 {
        ScCarryRound(mut s, 2 * j + 1)
    }
    return ScReduceLimbs(mut s)
}

// ScCanonical reports whether s < L, reading from the top byte down (RFC
// 8032 5.1.7 step 1). Variable time: it runs only on a signature's S,
// which is public.
ScCanonical(s: &[32]u8): bool {
    var i := 31
    for i >= 0 {
        l := ScL[i]
        if s[i] < l {
            return true
        }
        if s[i] > l {
            return false
        }
        i -= 1
    }
    return false
}

// ScL is L, little-endian.
const ScL: [32]u8 = [0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7, 0xa2, 0xde, 0xf9, 0xde, 0x14,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x10]

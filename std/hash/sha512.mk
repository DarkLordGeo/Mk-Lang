// Why: SHA-512 is the hash of Ed25519 and of HMAC-SHA-512, and faster than
// SHA-256 on 64-bit CPUs without SHA extensions.
//
// What: Sha512 (one-shot) and Sha512State (incremental), SHA-512 of FIPS
// 180-4 (sections 5.1.2 and 6.4); sha384.mk builds on them.
//
// How: 128-byte blocks, the message schedule expanded per block, 80 rounds
// unrolled by eight. Every word operation is mod 2^64 (+%). No allocation;
// the state is plain. Sha512Blocks first offers its whole blocks to
// std.simd.kernel's Sha512Blocks (the AVX2 schedule beside bmi2 rounds),
// which compresses them when the running CPU has avx2 and bmi2 and else
// reports false having written nothing; the portable loop then runs, and it
// is the reference. Both give the same bits. The offer sits in
// Sha512Blocks, so the binary holds one copy of the kernel's guard, as
// Sha256Compress does for SHA-256.

import std.simd.kernel

// Sha512K is FIPS 180-4's K (the cube roots of the first 80 primes).
const Sha512K: [80]u64 = [
    0x428a2f98d728ae22, 0x7137449123ef65cd, 0xb5c0fbcfec4d3b2f, 0xe9b5dba58189dbbc,
    0x3956c25bf348b538, 0x59f111f1b605d019, 0x923f82a4af194f9b, 0xab1c5ed5da6d8118,
    0xd807aa98a3030242, 0x12835b0145706fbe, 0x243185be4ee4b28c, 0x550c7dc3d5ffb4e2,
    0x72be5d74f27b896f, 0x80deb1fe3b1696b1, 0x9bdc06a725c71235, 0xc19bf174cf692694,
    0xe49b69c19ef14ad2, 0xefbe4786384f25e3, 0x0fc19dc68b8cd5b5, 0x240ca1cc77ac9c65,
    0x2de92c6f592b0275, 0x4a7484aa6ea6e483, 0x5cb0a9dcbd41fbd4, 0x76f988da831153b5,
    0x983e5152ee66dfab, 0xa831c66d2db43210, 0xb00327c898fb213f, 0xbf597fc7beef0ee4,
    0xc6e00bf33da88fc2, 0xd5a79147930aa725, 0x06ca6351e003826f, 0x142929670a0e6e70,
    0x27b70a8546d22ffc, 0x2e1b21385c26c926, 0x4d2c6dfc5ac42aed, 0x53380d139d95b3df,
    0x650a73548baf63de, 0x766a0abb3c77b2a8, 0x81c2c92e47edaee6, 0x92722c851482353b,
    0xa2bfe8a14cf10364, 0xa81a664bbc423001, 0xc24b8b70d0f89791, 0xc76c51a30654be30,
    0xd192e819d6ef5218, 0xd69906245565a910, 0xf40e35855771202a, 0x106aa07032bbd1b8,
    0x19a4c116b8d2d0c8, 0x1e376c085141ab53, 0x2748774cdf8eeb99, 0x34b0bcb5e19b48a8,
    0x391c0cb3c5c95a63, 0x4ed8aa4ae3418acb, 0x5b9cca4f7763e373, 0x682e6ff3d6b2b8a3,
    0x748f82ee5defb2fc, 0x78a5636f43172f60, 0x84c87814a1f0ab72, 0x8cc702081a6439ec,
    0x90befffa23631e28, 0xa4506cebde82bde9, 0xbef9a3f7b2c67915, 0xc67178f2e372532b,
    0xca273eceea26619c, 0xd186b8c721c0c207, 0xeada7dd6cde0eb1e, 0xf57d4f7fee6ed178,
    0x06f067aa72176fba, 0x0a637dc5a2c898a6, 0x113f9804bef90dae, 0x1b710b35131c471b,
    0x28db77f523047d84, 0x32caab7b40c72493, 0x3c9ebe0a15c9bebc, 0x431d67c49c100d4c,
    0x4cc5d4becb3e42b6, 0x597f299cfc657e2a, 0x5fcb6fab3ad6faec, 0x6c44198c4a475817,
]

/// Sha512State is a SHA-512 hash in progress. It is not zero-valid: a
/// zero-filled state is not the start of a hash, so
/// Arena.Slice[Sha512State] is E0701. Make one with New.
export struct Sha512State {
    h: [8]u64
    buf: [128]u8
    n: int
    total: u64
    invariant n >= 0 && n < 128
}

/// Returns the state of the empty message. O(1); allocates nothing.
export Sha512State.New(): Sha512State {
    return Sha512State(h: [0x6a09e667f3bcc908, 0xbb67ae8584caa73b, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1, 0x510e527fade682d1, 0x9b05688c2b3e6c1f, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179], buf: [0; 128], n: 0, total: 0)
}

// Sha512Round is one round on the working variables, written for the
// renaming schedule: d and h are updated in place, and the caller rotates
// the names (the next round's a is this round's h, its e this round's d),
// so eight calls in a row move no register. kw is K[t] + W[t].
Sha512Round(a: u64, b: u64, c: u64, d: mut u64, e: u64, f: u64, g: u64, h: mut u64, kw: u64) {
    t1 := *h +% (RotR64(e, 14) ^ RotR64(e, 18) ^ RotR64(e, 41)) +% (g ^ (e & (f ^ g))) +% kw
    *d = *d +% t1
    *h = t1 +% (RotR64(a, 28) ^ RotR64(a, 34) ^ RotR64(a, 39)) +% ((a & b) | (c & (a | b)))
}

// Sha512Blocks compresses every whole 128-byte block of data into h, in
// order; data.Len() % 128 trailing bytes are ignored. O(data.Len()). The
// schedule W is expanded per block into a local array first (as Go's and
// OpenSSL's generic code), then the rounds run unrolled by eight; unless
// the kernel took the blocks.
Sha512Blocks(h: mut [8]u64, data: []u8) {
    if kernel.Sha512Blocks(h, data) {
        return
    }
    n := data.Len()
    var i := 0
    var w: [80]u64 = [0; 80]
    for n - i >= 128 {
        blk := data[i..n][0..128]
        for t in 0..16 {
            w[t] = Be64(blk, 8 * t)
        }
        for t in 16..80 {
            x := w[t - 15]
            y := w[t - 2]
            s0 := RotR64(x, 1) ^ RotR64(x, 8) ^ (x >> 7)
            s1 := RotR64(y, 19) ^ RotR64(y, 61) ^ (y >> 6)
            w[t] = w[t - 16] +% s0 +% w[t - 7] +% s1
        }
        var a := h[0]
        var b := h[1]
        var c := h[2]
        var d := h[3]
        var e := h[4]
        var f := h[5]
        var g := h[6]
        var hh := h[7]
        for q in 0..10 {
            t := 8 * q
            Sha512Round(a, b, c, mut d, e, f, g, mut hh, Sha512K[t] +% w[t])
            Sha512Round(hh, a, b, mut c, d, e, f, mut g, Sha512K[t + 1] +% w[t + 1])
            Sha512Round(g, hh, a, mut b, c, d, e, mut f, Sha512K[t + 2] +% w[t + 2])
            Sha512Round(f, g, hh, mut a, b, c, d, mut e, Sha512K[t + 3] +% w[t + 3])
            Sha512Round(e, f, g, mut hh, a, b, c, mut d, Sha512K[t + 4] +% w[t + 4])
            Sha512Round(d, e, f, mut g, hh, a, b, mut c, Sha512K[t + 5] +% w[t + 5])
            Sha512Round(c, d, e, mut f, g, hh, a, mut b, Sha512K[t + 6] +% w[t + 6])
            Sha512Round(b, c, d, mut e, f, g, hh, mut a, Sha512K[t + 7] +% w[t + 7])
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

/// Absorbs data. Bytes in any split gives the same digest as Sha512 of the
/// concatenation. Whole blocks are hashed straight from data, without a
/// copy. O(data.Len()); allocates nothing.
export Sha512State.Bytes(mut self, data: []u8) {
    n := data.Len()
    self.total = self.total +% Trunc[u64](n)
    var i := 0
    if self.n > 0 {
        i = CopyBytes(mut self.buf[self.n..128], data)
        self.n += i
        if self.n < 128 {
            return
        }
        Sha512Blocks(mut self.h, self.buf[..])
        self.n = 0
    }
    whole := (n - i) - (n - i) % 128
    if whole > 0 {
        Sha512Blocks(mut self.h, data[i..i + whole])
        i += whole
    }
    if i < n {
        self.n = CopyBytes(mut self.buf[..], data[i..n])
    }
}

/// Returns the digest of everything absorbed. It pads a copy of the state,
/// so the state can keep absorbing. One or two block compressions;
/// allocates nothing.
export Sha512State.Sum(self): [64]u8 {
    var h := self.h
    var tail: [256]u8 = [0; 256]
    _ = CopyBytes(mut tail[..], self.buf[0..self.n])
    tail[self.n] = 0x80
    len := if self.n >= 112 { 256 } else { 128 }
    PutBe64(mut tail[..], len - 16, self.total >> 61)
    PutBe64(mut tail[..], len - 8, self.total << 3)
    Sha512Blocks(mut h, tail[0..len])
    var out: [64]u8 = [0; 64]
    for k in 0..8 {
        PutBe64(mut out[..], 8 * k, h[k])
    }
    return out
}

/// Returns the SHA-512 digest of data. O(data.Len()); allocates nothing.
export Sha512(data: []u8): [64]u8 {
    var s := Sha512State.New()
    s.Bytes(data)
    return s.Sum()
}

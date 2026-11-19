// Why: SHA-256 is the default cryptographic hash of TLS, signatures,
// content addressing and HMAC.
//
// What: Sha256 (one-shot) and Sha256State (incremental), SHA-256 of FIPS
// 180-4 (sections 5.1.1 and 6.2), and Sha256Compress.
//
// How: 64-byte blocks, the message schedule expanded per block, 64 rounds
// unrolled by eight. Every word operation is mod 2^32 (+%). No allocation;
// the state is plain. Whole blocks go through Sha256Compress, which first
// offers them to the instruction path (Sha256Kernel, sha256_kernel.mk) and
// runs its portable loop, the reference, when the CPU lacks it.

// Sha256K is FIPS 180-4's K (the cube roots of the first 64 primes).
const Sha256K: [64]u32 = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]

/// Sha256State is a SHA-256 hash in progress. It is not zero-valid: a
/// zero-filled state is not the start of a hash, so
/// Arena.Slice[Sha256State] is E0701. Make one with New.
export struct Sha256State {
    h: [8]u32
    buf: [64]u8
    n: int
    total: u64
    invariant n >= 0 && n < 64
}

/// Returns the state of the empty message. O(1); allocates nothing.
export Sha256State.New(): Sha256State {
    return Sha256State(h: [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19], buf: [0; 64], n: 0, total: 0)
}

// Sha256Round is one round on the working variables, written for the
// renaming schedule: d and h are updated in place, and the caller rotates
// the names (the next round's a is this round's h, its e this round's d),
// so eight calls in a row move no register. kw is K[t] + W[t].
Sha256Round(a: u32, b: u32, c: u32, d: mut u32, e: u32, f: u32, g: u32, h: mut u32, kw: u32) {
    t1 := *h +% (RotR32(e, 6) ^ RotR32(e, 11) ^ RotR32(e, 25)) +% (g ^ (e & (f ^ g))) +% kw
    *d = *d +% t1
    *h = t1 +% (RotR32(a, 2) ^ RotR32(a, 13) ^ RotR32(a, 22)) +% ((a & b) | (c & (a | b)))
}

// Sha256Compress compresses every whole 64-byte block of data into h, in
// order; data.Len() % 64 trailing bytes are ignored. O(data.Len()). The
// schedule W is expanded per block into a local array first (as Go's and
// OpenSSL's generic code), then the rounds run unrolled by eight. The loop
// is the portable path: constant time, and the reference the kernel
// matches. Its first line offers the blocks to the SHA extensions
// (sha256_kernel.mk), so the binary holds one copy of the kernel's guard.
Sha256Compress(h: mut [8]u32, data: []u8) {
    if Sha256Kernel(h, data) {
        return
    }
    n := data.Len()
    var i := 0
    var w: [64]u32 = [0; 64]
    for n - i >= 64 {
        blk := data[i..n][0..64]
        for t in 0..16 {
            w[t] = Be32(blk, 4 * t)
        }
        for t in 16..64 {
            x := w[t - 15]
            y := w[t - 2]
            s0 := RotR32(x, 7) ^ RotR32(x, 18) ^ (x >> 3)
            s1 := RotR32(y, 17) ^ RotR32(y, 19) ^ (y >> 10)
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
        for q in 0..8 {
            t := 8 * q
            Sha256Round(a, b, c, mut d, e, f, g, mut hh, Sha256K[t] +% w[t])
            Sha256Round(hh, a, b, mut c, d, e, f, mut g, Sha256K[t + 1] +% w[t + 1])
            Sha256Round(g, hh, a, mut b, c, d, e, mut f, Sha256K[t + 2] +% w[t + 2])
            Sha256Round(f, g, hh, mut a, b, c, d, mut e, Sha256K[t + 3] +% w[t + 3])
            Sha256Round(e, f, g, mut hh, a, b, c, mut d, Sha256K[t + 4] +% w[t + 4])
            Sha256Round(d, e, f, mut g, hh, a, b, mut c, Sha256K[t + 5] +% w[t + 5])
            Sha256Round(c, d, e, mut f, g, hh, a, mut b, Sha256K[t + 6] +% w[t + 6])
            Sha256Round(b, c, d, mut e, f, g, hh, mut a, Sha256K[t + 7] +% w[t + 7])
        }
        h[0] = h[0] +% a
        h[1] = h[1] +% b
        h[2] = h[2] +% c
        h[3] = h[3] +% d
        h[4] = h[4] +% e
        h[5] = h[5] +% f
        h[6] = h[6] +% g
        h[7] = h[7] +% hh
        i += 64
    }
}

/// Absorbs data. Bytes in any split gives the same digest as Sha256 of the
/// concatenation. Whole blocks are hashed straight from data, without a
/// copy. O(data.Len()); allocates nothing.
export Sha256State.Bytes(mut self, data: []u8) {
    n := data.Len()
    self.total = self.total +% Trunc[u64](n)
    var i := 0
    if self.n > 0 {
        i = CopyBytes(mut self.buf[self.n..64], data)
        self.n += i
        if self.n < 64 {
            return
        }
        Sha256Compress(mut self.h, self.buf[..])
        self.n = 0
    }
    whole := (n - i) - (n - i) % 64
    if whole > 0 {
        Sha256Compress(mut self.h, data[i..i + whole])
        i += whole
    }
    if i < n {
        self.n = CopyBytes(mut self.buf[..], data[i..n])
    }
}

/// Returns the digest of everything absorbed. The length in the padding is
/// exact below 2^61 bytes. It pads a copy of the state, so the state can
/// keep absorbing. One or two block compressions; allocates nothing.
export Sha256State.Sum(self): [32]u8 {
    var h := self.h
    var tail: [128]u8 = [0; 128]
    _ = CopyBytes(mut tail[..], self.buf[0..self.n])
    tail[self.n] = 0x80
    len := if self.n >= 56 { 128 } else { 64 }
    PutBe64(mut tail[..], len - 8, self.total << 3)
    Sha256Compress(mut h, tail[0..len])
    var out: [32]u8 = [0; 32]
    for k in 0..8 {
        PutBe32(mut out[..], 4 * k, h[k])
    }
    return out
}

/// Returns the SHA-256 digest of data. It uses the CPU's SHA instructions
/// where they exist; both paths give the same digest and run in time that
/// depends only on data's length. O(data.Len()); allocates nothing.
export Sha256(data: []u8): [32]u8 {
    var s := Sha256State.New()
    s.Bytes(data)
    return s.Sum()
}

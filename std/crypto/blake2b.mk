// Why: Argon2id is built on BLAKE2b, which std.hash does not offer, so
// std.crypto carries its own private copy.
//
// What: B2b (B2New, Bytes, Sum), the one-shot B2Hash and the round function
// B2Compress with B2Mix.
//
// How: BLAKE2b of RFC 7693 with no key: the hash H^x and the round function
// that Argon2id (argon2.mk, RFC 9106 sections 3.2 and 3.6) is built on. H^x
// is BLAKE2b with its output length x in the parameter block (nn = x), not
// BLAKE2b-512 cut short. The byte counter is one word: no input Argon2 hashes
// reaches 2^64 bytes, so the high word of RFC 7693's t is always zero.
// Constant time: twelve rounds of +%, ^ and rotations by constants over each
// block, with no branch or index on the data.

import std.raw

// B2Iv is RFC 7693 section 2.6's IV, SHA-512's.
const B2Iv: [8]u64 = [
    0x6a09e667f3bcc908, 0xbb67ae8584caa73b, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
    0x510e527fade682d1, 0x9b05688c2b3e6c1f, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
]

// B2Sigma is RFC 7693 section 2.7's message schedule, ten rows of sixteen;
// rounds 10 and 11 reuse rows 0 and 1.
const B2Sigma: [160]u8 = [
    0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
    14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3,
    11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4,
    7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8,
    9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13,
    2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9,
    12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11,
    13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10,
    6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5,
    10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0,
]

// B2b is a BLAKE2b computation in progress (RFC 7693 section 3.3): the
// chaining value h, its eight words little-endian, the n bytes of the
// block not yet compressed in buf (a full block waits there until more
// input or Sum shows whether it is the last), the bytes compressed so far
// in t, and the output length. h and buf are bytes so that Sum can wipe
// them: h is H_0 itself in Argon2id's step 1, and buf the end of its
// input, password and salt included. v is the compression's working
// vector, in the state so that a test sees B2Compress wipe it.
struct B2b {
    h: [64]u8
    buf: [128]u8
    v: [128]u8
    n: int
    t: u64
    outlen: int
}

// B2New is the state of BLAKE2b with an outlen-byte output and no key, 1
// <= outlen <= 64: h is the IV with the parameter block's first word,
// 0x0101kknn with kk = 0, XORed in. O(1); allocates nothing.
B2New(outlen: int): B2b {
    var h: [64]u8 = [0; 64]
    for i in 0..8 {
        PutLe64(mut h[..], 8 * i, B2Iv[i])
    }
    PutLe64(mut h[..], 0, B2Iv[0] ^ 0x01010000 ^ Trunc[u64](outlen))
    return B2b(h: h, buf: [0; 128], v: [0; 128], n: 0, t: 0, outlen: outlen)
}

// B2b.Bytes absorbs data. A full buffer is compressed only when more input
// follows, so the last block is always Sum's. O(data.Len()); allocates
// nothing.
B2b.Bytes(mut self, data: []u8) {
    n := data.Len()
    var i := 0
    for i < n {
        if self.n == 128 {
            self.t = self.t +% 128
            B2Compress(mut self.h, mut self.v, self.buf[..], self.t, false)
            self.n = 0
        }
        var k := 128 - self.n
        if k > n - i {
            k = n - i
        }
        (mut self.buf[self.n..self.n + k]).CopyFrom(data[i..i + k])
        self.n += k
        i += k
    }
}

// B2b.Sum writes the digest to out, which holds exactly outlen bytes: the
// buffered bytes, zero-padded, compressed with the final flag, and the
// first outlen bytes of h. The state is spent: h and buf are wiped with
// Wipe, as the reference's blake2b_final clears its state. One block
// compression; allocates nothing.
B2b.Sum(mut self, out: mut []u8) {
    self.t = self.t +% Trunc[u64](self.n)
    for i in self.n..128 {
        self.buf[i] = 0
    }
    B2Compress(mut self.h, mut self.v, self.buf[..], self.t, true)
    (mut out[..]).CopyFrom(self.h[0..self.outlen])
    Wipe(mut self.h[..])
    Wipe(mut self.buf[..])
}

// B2Hash writes BLAKE2b of data with an out.Len()-byte output to out, 1 <=
// out.Len() <= 64 (RFC 9106's H^x with x = out.Len()). O(data.Len());
// allocates nothing.
B2Hash(out: mut []u8, data: []u8) {
    var s := B2New(out.Len())
    s.Bytes(data)
    s.Sum(mut out[..])
}

// B2Rot rotates x right by n, 0 < n < 64; n is a constant at every call.
B2Rot(x: u64, n: int): u64 {
    return raw.RotL64(x, 64 - n)
}

// B2Mix is RFC 7693 section 3.1's G on the words a, b, c and d of the
// working vector v, with the message words x and y of the block m.
B2Mix(v: mut [128]u8, m: []u8, a: int, b: int, c: int, d: int, x: int, y: int) {
    var va := Le64(v[..], 8 * a)
    var vb := Le64(v[..], 8 * b)
    var vc := Le64(v[..], 8 * c)
    var vd := Le64(v[..], 8 * d)
    va = va +% vb +% Le64(m, 8 * x)
    vd = B2Rot(vd ^ va, 32)
    vc = vc +% vd
    vb = B2Rot(vb ^ vc, 24)
    va = va +% vb +% Le64(m, 8 * y)
    vd = B2Rot(vd ^ va, 16)
    vc = vc +% vd
    vb = B2Rot(vb ^ vc, 63)
    PutLe64(mut v[..], 8 * a, va)
    PutLe64(mut v[..], 8 * b, vb)
    PutLe64(mut v[..], 8 * c, vc)
    PutLe64(mut v[..], 8 * d, vd)
}

// B2Compress is RFC 7693 section 3.2's F: the 128-byte block blk into h,
// with t bytes counted and last the final-block flag (both public). The
// message words are read from blk where they are, and the working vector
// v, from which h follows, is the caller's bytes (B2b.v), wiped before it
// returns.
B2Compress(h: mut [64]u8, v: mut [128]u8, blk: []u8, t: u64, last: bool) {
    (mut v[0..64]).CopyFrom(h[..])
    for i in 0..8 {
        PutLe64(mut v[..], 64 + 8 * i, B2Iv[i])
    }
    PutLe64(mut v[..], 96, B2Iv[4] ^ t)
    if last {
        PutLe64(mut v[..], 112, ~B2Iv[6])
    }
    for r in 0..12 {
        s := 16 * (r % 10)
        B2Mix(v, blk, 0, 4, 8, 12, int(B2Sigma[s]), int(B2Sigma[s + 1]))
        B2Mix(v, blk, 1, 5, 9, 13, int(B2Sigma[s + 2]), int(B2Sigma[s + 3]))
        B2Mix(v, blk, 2, 6, 10, 14, int(B2Sigma[s + 4]), int(B2Sigma[s + 5]))
        B2Mix(v, blk, 3, 7, 11, 15, int(B2Sigma[s + 6]), int(B2Sigma[s + 7]))
        B2Mix(v, blk, 0, 5, 10, 15, int(B2Sigma[s + 8]), int(B2Sigma[s + 9]))
        B2Mix(v, blk, 1, 6, 11, 12, int(B2Sigma[s + 10]), int(B2Sigma[s + 11]))
        B2Mix(v, blk, 2, 7, 8, 13, int(B2Sigma[s + 12]), int(B2Sigma[s + 13]))
        B2Mix(v, blk, 3, 4, 9, 14, int(B2Sigma[s + 14]), int(B2Sigma[s + 15]))
    }
    for i in 0..8 {
        PutLe64(mut h[..], 8 * i, Le64(h[..], 8 * i) ^ Le64(v[..], 8 * i) ^ Le64(v[..], 64 + 8 * i))
    }
    Wipe(mut v[..])
}

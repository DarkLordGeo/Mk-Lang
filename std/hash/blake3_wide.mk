// Why: BLAKE3's chunks are independent, so four can be compressed at once
// with SIMD for most of the speed of a long input.
//
// What: the 4-chunk leaf that Blake3 and Blake3State.Bytes use for whole
// chunks.
//
// How: after the blake3 crate's hash_many and the C implementation's
// blake3_hash4_sse41: chunk j sits in lane j of each of the 16 state words,
// so one std.simd operation does the work of four scalar ones. The message
// is transposed once per block (two interleaves and two shuffles per four
// words). Every lane operation is the scalar compression's, mod 2^32 (+%),
// so the result is bit-identical to Blake3Chunk on each chunk; the scalar
// path stays the reference. No allocation, no unsafe: std.simd's baseline
// width needs no cpu clause. Blake3Subtree and Blake3State.Bytes call it
// only when Target.os is not Freestanding, so a general-regs-only bin never
// instantiates it (E1108).

import std.simd

// Blake3RotRV rotates each lane of x right by n.
Blake3RotRV(x: simd.Vec[u32, 4], const n: int): simd.Vec[u32, 4] {
    return (x >> n) | (x << (32 - n))
}

// Blake3GV is Blake3G on four chunks at once, one lane each.
Blake3GV(s: mut [16]simd.Vec[u32, 4], a: int, b: int, c: int, d: int, x: simd.Vec[u32, 4], y: simd.Vec[u32, 4]) {
    s[a] = s[a] +% s[b] +% x
    s[d] = Blake3RotRV(s[d] ^ s[a], 16)
    s[c] = s[c] +% s[d]
    s[b] = Blake3RotRV(s[b] ^ s[c], 12)
    s[a] = s[a] +% s[b] +% y
    s[d] = Blake3RotRV(s[d] ^ s[a], 8)
    s[c] = s[c] +% s[d]
    s[b] = Blake3RotRV(s[b] ^ s[c], 7)
}

// Blake3RoundV is Blake3Round on four chunks at once, on the message
// vectors x0 to x15 in their order for this round.
Blake3RoundV(s: mut [16]simd.Vec[u32, 4], x0: simd.Vec[u32, 4], x1: simd.Vec[u32, 4], x2: simd.Vec[u32, 4], x3: simd.Vec[u32, 4],
    x4: simd.Vec[u32, 4], x5: simd.Vec[u32, 4], x6: simd.Vec[u32, 4], x7: simd.Vec[u32, 4], x8: simd.Vec[u32, 4], x9: simd.Vec[u32, 4],
    x10: simd.Vec[u32, 4], x11: simd.Vec[u32, 4], x12: simd.Vec[u32, 4], x13: simd.Vec[u32, 4], x14: simd.Vec[u32, 4], x15: simd.Vec[u32, 4]) {
    Blake3GV(s, 0, 4, 8, 12, x0, x1)
    Blake3GV(s, 1, 5, 9, 13, x2, x3)
    Blake3GV(s, 2, 6, 10, 14, x4, x5)
    Blake3GV(s, 3, 7, 11, 15, x6, x7)
    Blake3GV(s, 0, 5, 10, 15, x8, x9)
    Blake3GV(s, 1, 6, 11, 12, x10, x11)
    Blake3GV(s, 2, 7, 8, 13, x12, x13)
    Blake3GV(s, 3, 4, 9, 14, x14, x15)
}

// Blake3Load4 transposes words 4q to 4q + 3 of block b of the four chunks
// of data into m[4q] to m[4q + 3]: lane j of m[w] is word w of chunk j's
// block, read little-endian.
Blake3Load4(m: mut [16]simd.Vec[u32, 4], data: []u8, b: int, q: int) {
    at := 64 * b + 16 * q
    w0 := simd.Vec[u8, 16].FromSlice(data, at).Reinterpret[u32, 4]()
    w1 := simd.Vec[u8, 16].FromSlice(data, 1024 + at).Reinterpret[u32, 4]()
    w2 := simd.Vec[u8, 16].FromSlice(data, 2048 + at).Reinterpret[u32, 4]()
    w3 := simd.Vec[u8, 16].FromSlice(data, 3072 + at).Reinterpret[u32, 4]()
    t0 := w0.InterleaveLo(w1)
    t1 := w2.InterleaveLo(w3)
    t2 := w0.InterleaveHi(w1)
    t3 := w2.InterleaveHi(w3)
    m[4 * q] = t0.Shuffle(t1, [0, 1, 4, 5])
    m[4 * q + 1] = t0.Shuffle(t1, [2, 3, 6, 7])
    m[4 * q + 2] = t2.Shuffle(t3, [0, 1, 4, 5])
    m[4 * q + 3] = t2.Shuffle(t3, [2, 3, 6, 7])
}

// Blake3Chunks4 is the chaining values of the four whole chunks of data
// (exactly 4,096 bytes), whose first chunk counter is chunk: 16 blocks of
// seven rounds, four chunks per operation. The counters are built from
// four scalar sums, so a carry into the high word is the scalar path's.
// O(1) (4,096 bytes); allocates nothing.
Blake3Chunks4(data: []u8, chunk: u64): [4][8]u32 {
    d := data[0..4096]
    c0 := chunk
    c1 := chunk +% 1
    c2 := chunk +% 2
    c3 := chunk +% 3
    lo := simd.Vec[u32, 4].FromArray([Trunc[u32](c0), Trunc[u32](c1), Trunc[u32](c2), Trunc[u32](c3)])
    hi := simd.Vec[u32, 4].FromArray([Trunc[u32](c0 >> 32), Trunc[u32](c1 >> 32), Trunc[u32](c2 >> 32), Trunc[u32](c3 >> 32)])
    var h: [8]simd.Vec[u32, 4] = [simd.Vec[u32, 4].Zero(); 8]
    for k in 0..8 {
        h[k] = simd.Vec[u32, 4].Splat(Blake3IV[k])
    }
    var m: [16]simd.Vec[u32, 4] = [simd.Vec[u32, 4].Zero(); 16]
    for b in 0..16 {
        Blake3Load4(mut m, d, b, 0)
        Blake3Load4(mut m, d, b, 1)
        Blake3Load4(mut m, d, b, 2)
        Blake3Load4(mut m, d, b, 3)
        var flags: u32 = 0
        if b == 0 {
            flags = Blake3ChunkStart
        }
        if b == 15 {
            flags = Blake3ChunkEnd
        }
        var s: [16]simd.Vec[u32, 4] = [h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7],
            simd.Vec[u32, 4].Splat(Blake3IV[0]), simd.Vec[u32, 4].Splat(Blake3IV[1]),
            simd.Vec[u32, 4].Splat(Blake3IV[2]), simd.Vec[u32, 4].Splat(Blake3IV[3]),
            lo, hi, simd.Vec[u32, 4].Splat(64), simd.Vec[u32, 4].Splat(flags)]
        Blake3RoundV(mut s, m[0], m[1], m[2], m[3], m[4], m[5], m[6], m[7], m[8], m[9], m[10], m[11], m[12], m[13], m[14], m[15])
        Blake3RoundV(mut s, m[2], m[6], m[3], m[10], m[7], m[0], m[4], m[13], m[1], m[11], m[12], m[5], m[9], m[14], m[15], m[8])
        Blake3RoundV(mut s, m[3], m[4], m[10], m[12], m[13], m[2], m[7], m[14], m[6], m[5], m[9], m[0], m[11], m[15], m[8], m[1])
        Blake3RoundV(mut s, m[10], m[7], m[12], m[9], m[14], m[3], m[13], m[15], m[4], m[0], m[11], m[2], m[5], m[8], m[1], m[6])
        Blake3RoundV(mut s, m[12], m[13], m[9], m[11], m[15], m[10], m[14], m[8], m[7], m[2], m[5], m[3], m[0], m[1], m[6], m[4])
        Blake3RoundV(mut s, m[9], m[14], m[11], m[5], m[8], m[12], m[15], m[1], m[13], m[3], m[0], m[10], m[2], m[6], m[4], m[7])
        Blake3RoundV(mut s, m[11], m[15], m[5], m[0], m[1], m[9], m[8], m[6], m[14], m[10], m[2], m[12], m[3], m[4], m[7], m[13])
        for k in 0..8 {
            h[k] = s[k] ^ s[k + 8]
        }
    }
    var out: [4][8]u32 = [[0; 8]; 4]
    for k in 0..8 {
        a := h[k].ToArray()
        for j in 0..4 {
            out[j][k] = a[j]
        }
    }
    return out
}

// Blake3Subtree4 is the chaining value of the 4-chunk subtree over data
// (exactly 4,096 bytes) whose first chunk counter is chunk: the four leaves
// at once, then the two parents and the top one, which carries root.
Blake3Subtree4(data: []u8, chunk: u64, root: u32): [8]u32 {
    cv := Blake3Chunks4(data, chunk)
    pl := Blake3ParentWords(&cv[0], &cv[1])
    l := Blake3Cv(&Blake3IV, &pl, 0, 64, Blake3Parent)
    pr := Blake3ParentWords(&cv[2], &cv[3])
    r := Blake3Cv(&Blake3IV, &pr, 0, 64, Blake3Parent)
    p := Blake3ParentWords(&l, &r)
    return Blake3Cv(&Blake3IV, &p, 0, 64, Blake3Parent | root)
}

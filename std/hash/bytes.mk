// Why: every hash reads and writes words of a byte slice; doing it without
// unsafe keeps std.hash free of unsafe blocks.
//
// What: the little- and big-endian word loads and stores the hashes of this
// module are built on.
//
// How: a load reads a sub-slice of constant length (b[i..n][0..8]) byte by
// byte; LLVM folds the eight byte loads into one unaligned load, so a load
// costs the two compares of the sub-slice, and none at a constant offset
// into a block taken once. An i with i < 0 or i + 8 > b.Len() is a bounds
// panic (code 2).

import std.raw

// Le64 is the 8 bytes of b at i as a little-endian word.
Le64(b: []u8, i: int): u64 {
    w := b[i..b.Len()][0..8]
    return u64(w[0]) | (u64(w[1]) << 8) | (u64(w[2]) << 16) | (u64(w[3]) << 24) |
        (u64(w[4]) << 32) | (u64(w[5]) << 40) | (u64(w[6]) << 48) | (u64(w[7]) << 56)
}

// Le32 is the 4 bytes of b at i as a little-endian word.
Le32(b: []u8, i: int): u32 {
    w := b[i..b.Len()][0..4]
    return u32(w[0]) | (u32(w[1]) << 8) | (u32(w[2]) << 16) | (u32(w[3]) << 24)
}

// Be32 is the 4 bytes of b at i as a big-endian word (one load and bswap).
Be32(b: []u8, i: int): u32 {
    w := b[i..b.Len()][0..4]
    return (u32(w[0]) << 24) | (u32(w[1]) << 16) | (u32(w[2]) << 8) | u32(w[3])
}

// Be64 is the 8 bytes of b at i as a big-endian word.
Be64(b: []u8, i: int): u64 {
    w := b[i..b.Len()][0..8]
    return (u64(w[0]) << 56) | (u64(w[1]) << 48) | (u64(w[2]) << 40) | (u64(w[3]) << 32) |
        (u64(w[4]) << 24) | (u64(w[5]) << 16) | (u64(w[6]) << 8) | u64(w[7])
}

// PutBe32 stores x big-endian at out[i..i + 4].
PutBe32(out: mut []u8, i: int, x: u32) {
    t := mut out[i..out.Len()]
    w := mut t[0..4]
    w[0] = Trunc[u8](x >> 24)
    w[1] = Trunc[u8](x >> 16)
    w[2] = Trunc[u8](x >> 8)
    w[3] = Trunc[u8](x)
}

// PutBe64 stores x big-endian at out[i..i + 8].
PutBe64(out: mut []u8, i: int, x: u64) {
    t := mut out[i..out.Len()]
    w := mut t[0..8]
    w[0] = Trunc[u8](x >> 56)
    w[1] = Trunc[u8](x >> 48)
    w[2] = Trunc[u8](x >> 40)
    w[3] = Trunc[u8](x >> 32)
    w[4] = Trunc[u8](x >> 24)
    w[5] = Trunc[u8](x >> 16)
    w[6] = Trunc[u8](x >> 8)
    w[7] = Trunc[u8](x)
}

// PutLe32 stores x little-endian at out[i..i + 4].
PutLe32(out: mut []u8, i: int, x: u32) {
    t := mut out[i..out.Len()]
    w := mut t[0..4]
    w[0] = Trunc[u8](x)
    w[1] = Trunc[u8](x >> 8)
    w[2] = Trunc[u8](x >> 16)
    w[3] = Trunc[u8](x >> 24)
}

// RotR32 rotates x right by n, 0 < n < 32 (a constant at every call, so
// LLVM emits one ror). Bits shifted out of << are dropped.
RotR32(x: u32, n: int): u32 {
    return (x >> n) | (x << (32 - n))
}

// RotR64 rotates x right by n, 0 < n < 64.
RotR64(x: u64, n: int): u64 {
    return raw.RotL64(x, 64 - n)
}

// Fold is the 128-bit product of a and b, its high word xor its low word
// (XXH3's mul128_fold64).
Fold(a: u64, b: u64): u64 {
    p := raw.Mul128(a, b)
    return p.hi ^ p.lo
}

// CopyBytes copies min(dst.Len(), src.Len()) bytes from src to the start of dst
// and returns the count.
CopyBytes(dst: mut []u8, src: []u8): int {
    n := if src.Len() < dst.Len() { src.Len() } else { dst.Len() }
    for i in 0..n {
        dst[i] = src[i]
    }
    return n
}

// Why: every primitive reads and writes words in a fixed byte order, and
// std.crypto takes no unsafe code to do it.
//
// What: Le64, Le32, Be64, Be32 and their Put forms.
//
// How: the word loads and stores of std.crypto, with no unsafe (std.hash's
// idiom: a sub-slice of constant length read byte by byte, which LLVM folds
// to one load). An i with i < 0 or i + 8 > b.Len() is a bounds panic.

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

// PutLe64 stores x little-endian at out[i..i + 8].
PutLe64(out: mut []u8, i: int, x: u64) {
    t := mut out[i..out.Len()]
    w := mut t[0..8]
    w[0] = Trunc[u8](x)
    w[1] = Trunc[u8](x >> 8)
    w[2] = Trunc[u8](x >> 16)
    w[3] = Trunc[u8](x >> 24)
    w[4] = Trunc[u8](x >> 32)
    w[5] = Trunc[u8](x >> 40)
    w[6] = Trunc[u8](x >> 48)
    w[7] = Trunc[u8](x >> 56)
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

// Be64 is the 8 bytes of b at i as a big-endian word.
Be64(b: []u8, i: int): u64 {
    w := b[i..b.Len()][0..8]
    return (u64(w[0]) << 56) | (u64(w[1]) << 48) | (u64(w[2]) << 40) | (u64(w[3]) << 32) |
        (u64(w[4]) << 24) | (u64(w[5]) << 16) | (u64(w[6]) << 8) | u64(w[7])
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

// Be32 is the 4 bytes of b at i as a big-endian word.
Be32(b: []u8, i: int): u32 {
    w := b[i..b.Len()][0..4]
    return (u32(w[0]) << 24) | (u32(w[1]) << 16) | (u32(w[2]) << 8) | u32(w[3])
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

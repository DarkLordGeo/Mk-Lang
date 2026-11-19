// Why: the log's and the snapshot's file formats store little-endian
// integers in byte slices.
// What: PutU16, PutU32, PutU64 and U16, U32, U64.
// How: byte by byte with shifts; each panics on an index past the slice,
// as indexing does.

/// Writes `x` at `b[at..at + 2]`.
export PutU16(b: mut []u8, at: int, x: u16) {
    b[at] = Trunc[u8](x)
    b[at + 1] = Trunc[u8](x >> 8)
}

/// Writes `x` at `b[at..at + 4]`.
export PutU32(b: mut []u8, at: int, x: u32) {
    for i in 0..4 {
        b[at + i] = Trunc[u8](x >> Cast[u32](8 * i))
    }
}

/// Writes `x` at `b[at..at + 8]`.
export PutU64(b: mut []u8, at: int, x: u64) {
    for i in 0..8 {
        b[at + i] = Trunc[u8](x >> Cast[u64](8 * i))
    }
}

/// Reads the `u16` at `b[at..at + 2]`.
export U16(b: []u8, at: int): u16 {
    return u16(b[at]) | u16(b[at + 1]) << 8
}

/// Reads the `u32` at `b[at..at + 4]`.
export U32(b: []u8, at: int): u32 {
    var x: u32 = 0
    for i in 0..4 {
        x |= u32(b[at + i]) << Cast[u32](8 * i)
    }
    return x
}

/// Reads the `u64` at `b[at..at + 8]`.
export U64(b: []u8, at: int): u64 {
    var x: u64 = 0
    for i in 0..8 {
        x |= u64(b[at + i]) << Cast[u64](8 * i)
    }
    return x
}

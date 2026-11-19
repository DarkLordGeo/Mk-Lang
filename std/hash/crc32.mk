// Why: CRC-32 is the checksum of zlib, gzip, PNG and Ethernet; formats need
// it bit-exact and fast.
//
// What: Crc32 (one-shot) and Crc32State (incremental), CRC-32/ISO-HDLC
// (reflected polynomial 0xEDB88320, init and xorout 0xFFFFFFFF, check value
// 0xCBF43926 for "123456789").
//
// How: slicing-by-16 tables (Kounavis and Berry, Intel 2005/2008, as Rust
// crc32fast's table path; zlib and Go use slicing-by-8 or braids). From 64
// bytes, CrcRun first offers the input to std.simd.kernel's Crc32Fold
// (carry-less multiply folding), which folds its whole 16-byte blocks when
// the running CPU has pclmul and aes, else reports 0 having written
// nothing; the table then runs over the folded 16 bytes and the tail, or
// over everything. Both paths give the same CRC; the table path is the
// reference, and the evaluator's (Cpu() is the baseline there). std.hash
// holds no unsafe block: the kernel's own guard carries the obligation.

import std.simd.kernel

// CrcTables builds the sixteen 256-entry tables of slicing-by-16: table 0 is
// the byte-at-a-time table, and entry i of table k is the CRC of byte i
// followed by k zero bytes. It runs once, in the evaluator, for Crc16.
CrcTables(): [4096]u32 {
    var t: [4096]u32 = [0; 4096]
    for i in 0..256 {
        var c: u32 = Trunc[u32](i)
        for _ in 0..8 {
            c = if c & 1 != 0 { (c >> 1) ^ 0xEDB88320 } else { c >> 1 }
        }
        t[i] = c
    }
    for i in 0..256 {
        var c := t[i]
        for k in 1..16 {
            c = t[int(c & 0xFF)] ^ (c >> 8)
            t[k * 256 + i] = c
        }
    }
    return t
}

// Crc16 is table k at Crc16[256 * k..256 * (k + 1)], computed at compile
// time (16 KiB).
const Crc16: [4096]u32 = CrcTables()

/// Returns the CRC-32 of data (CRC-32/ISO-HDLC), the checksum of gzip, PNG,
/// ZIP and Ethernet. Cannot fail. O(n); allocates nothing.
export Crc32(data: []u8): u32 {
    return CrcRun(0xFFFFFFFF, data) ^ 0xFFFFFFFF
}

/// Crc32State is a CRC-32 in progress. Its zero value is the CRC of the
/// empty message, so a zero-filled Crc32State is a fresh one: the type is
/// zero-valid.
export struct Crc32State {
    crc: u32
}

/// Returns the state of the empty message. O(1); allocates nothing.
export Crc32State.New(): Crc32State {
    return Crc32State(crc: 0)
}

/// Absorbs data. Bytes in any split gives the same CRC as Crc32 of the
/// concatenation. O(data.Len()); allocates nothing and copies nothing.
export Crc32State.Bytes(mut self, data: []u8) {
    self.crc = CrcRun(self.crc ^ 0xFFFFFFFF, data) ^ 0xFFFFFFFF
}

/// Returns the CRC-32 of everything absorbed; the state can keep absorbing.
/// O(1); allocates nothing.
export Crc32State.Sum(self): u32 {
    return self.crc
}

// CrcRun runs the register crc (pre-inverted) over data: through the
// kernel's fold when it takes the input (64 bytes or more, and the CPU's
// pclmul and aes), then the table over the fold's 16 bytes from a zero
// register and over the bytes the fold left; else the table over all of
// data.
CrcRun(crc: u32, data: []u8): u32 {
    if data.Len() >= 64 {
        var f: [16]u8 = [0; 16]
        k := kernel.Crc32Fold(crc, data, mut f)
        if k > 0 {
            return CrcUpdate(CrcUpdate(0, f[..]), data[k..data.Len()])
        }
    }
    return CrcUpdate(crc, data)
}

// CrcUpdate runs the register c (pre-inverted) over data: one 16-byte step
// per 16 bytes (four word loads from a 16-byte sub-slice, sixteen table
// loads; no bounds check is left on a table load, since every index is a
// byte plus a constant), one 8-byte step, then one table step per tail
// byte. Only the four terms of a = c ^ word 0 depend on c, so they are
// xored last, as Go's crc32_generic.go and crc32fast order them: the
// twelve other loads and xors run off the loop-carried chain, which is one
// xor, four parallel table loads and four xors per 16 bytes (with those
// four terms first, all sixteen xors are on the chain, as the release
// disassembly shows).
// The table is read through one reference t: the evaluator charges a read
// of the global const far more fuel than a read through a reference, and
// native code is the same either way.
CrcUpdate(crc: u32, data: []u8): u32 {
    t := &Crc16
    var c := crc
    var i := 0
    n := data.Len()
    for n - i >= 16 {
        w := data[i..n][0..16]
        a := c ^ Le32(w, 0)
        b := Le32(w, 4)
        d := Le32(w, 8)
        e := Le32(w, 12)
        // The twelve terms of b, d and e first: they do not depend on c.
        c = t[2816 + int(b & 0xFF)] ^ t[2560 + int((b >> 8) & 0xFF)] ^
            t[2304 + int((b >> 16) & 0xFF)] ^ t[2048 + int(b >> 24)] ^
            t[1792 + int(d & 0xFF)] ^ t[1536 + int((d >> 8) & 0xFF)] ^
            t[1280 + int((d >> 16) & 0xFF)] ^ t[1024 + int(d >> 24)] ^
            t[768 + int(e & 0xFF)] ^ t[512 + int((e >> 8) & 0xFF)] ^
            t[256 + int((e >> 16) & 0xFF)] ^ t[int(e >> 24)] ^
            t[3840 + int(a & 0xFF)] ^ t[3584 + int((a >> 8) & 0xFF)] ^
            t[3328 + int((a >> 16) & 0xFF)] ^ t[3072 + int(a >> 24)]
        i += 16
    }
    if n - i >= 8 {
        w := data[i..n][0..8]
        a := c ^ Le32(w, 0)
        b := Le32(w, 4)
        c = t[768 + int(b & 0xFF)] ^ t[512 + int((b >> 8) & 0xFF)] ^
            t[256 + int((b >> 16) & 0xFF)] ^ t[int(b >> 24)] ^
            t[1792 + int(a & 0xFF)] ^ t[1536 + int((a >> 8) & 0xFF)] ^
            t[1280 + int((a >> 16) & 0xFF)] ^ t[1024 + int(a >> 24)]
        i += 8
    }
    for i < n {
        c = t[int((c ^ u32(data[i])) & 0xFF)] ^ (c >> 8)
        i += 1
    }
    return c
}

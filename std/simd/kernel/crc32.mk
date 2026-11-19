// Why: CRC-32 checks every zip, gzip and PNG stream, and a table loop is far
// slower than folding with the CPU's carry-less multiply.
//
// What: Crc32Fold, the CRC-32 folding kernel behind its simd.Cpu() guard, and
// CrcFoldK, its folding constants.
//
// How: the folding of Gopal et al., "Fast CRC Computation for Generic
// Polynomials Using PCLMULQDQ Instruction" (Intel, 2009), for
// CRC-32/ISO-HDLC's reflected polynomial 0xEDB88320, in the shape of Go's
// ieeeCLMUL and Rust crc32fast's pclmulqdq.rs: four 128-bit accumulators
// folded 64 bytes at a time, then into one, then 16 bytes at a time.
//
// A 16-byte block is held as Vec[u64, 2], lane 0 the little-endian word of
// bytes 0 to 7. Folding a block a over a distance of d bits onto b is
// b ^ clmul(a.lo, k(d + 32)) ^ clmul(a.hi, k(d - 32)), with k(n) the
// reflected x^n mod P (CrcFoldK). Every fold keeps the invariant that the
// CRC register from zero over the accumulator's 16 bytes equals the
// register c over the bytes consumed so far. So the kernel stops there and
// hands the 16 bytes back: std.hash runs its table over them, and over the
// tail, and needs neither the paper's 64-bit step nor its Barrett
// reduction. Each cpu clause names sse2 too: PCLMULQDQ works in xmm
// registers (E1106 needs on x86_64; aarch64 ignores the name).

import std.raw
import std.simd

// CrcFoldK is x^n mod P(x) for CRC-32's polynomial P = 0x104C11DB7,
// bit-reflected in 32 bits and shifted left by one: Gopal et al.'s
// constant for a fold whose clmul term ends n bits ahead. A const
// initializer calls it, so it runs only in the evaluator.
CrcFoldK(n: int): u64 {
    var r: u64 = 1
    for _ in 0..n {
        r = r << 1
        if r & 0x100000000 != 0 {
            r = r ^ 0x104C11DB7
        }
    }
    var f: u64 = 0
    for b in 0..32 {
        if (r >> b) & 1 != 0 {
            f = f | (u64(1) << (31 - b))
        }
    }
    return f << 1
}

// CrcK1 and CrcK2 fold a block onto the one 64 bytes on (the four-way
// loop); CrcK3 and CrcK4 fold it onto the next block. They are
// 0x154442bd4, 0x1c6e41596, 0x1751997d0 and 0x0ccaa009e, crc32fast's K1 to
// K4.
const CrcK1: u64 = CrcFoldK(4 * 128 + 32)
const CrcK2: u64 = CrcFoldK(4 * 128 - 32)
const CrcK3: u64 = CrcFoldK(128 + 32)
const CrcK4: u64 = CrcFoldK(128 - 32)

// CrcBlock is the 16 bytes of data from i, one bounds check.
CrcBlock(data: []u8, i: int): simd.Vec[u64, 2] {
    return simd.Vec[u8, 16].FromSlice(data, i).Reinterpret[u64, 2]()
}

// CrcFold is a folded onto b with the constants k1 (for a's low word) and
// k2 (its high word).
CrcFold(a: simd.Vec[u64, 2], b: simd.Vec[u64, 2], k1: u64, k2: u64): simd.Vec[u64, 2] cpu "sse2, pclmul, aes" {
    p := raw.Clmul64(a.Lane(0), k1)
    q := raw.Clmul64(a.Lane(1), k2)
    return b ^ simd.Vec[u64, 2].FromArray([p.lo ^ q.lo, p.hi ^ q.hi])
}

// Crc32Ni is Crc32Fold's body, for data.Len() >= 64.
Crc32Ni(c: u32, data: []u8, fold: mut [16]u8): int cpu "sse2, pclmul, aes" {
    n := data.Len() / 16 * 16
    var x3 := CrcBlock(data, 0) ^ simd.Vec[u64, 2].FromArray([u64(c), 0])
    var x2 := CrcBlock(data, 16)
    var x1 := CrcBlock(data, 32)
    var x0 := CrcBlock(data, 48)
    var i := 64
    for n - i >= 64 {
        x3 = CrcFold(x3, CrcBlock(data, i), CrcK1, CrcK2)
        x2 = CrcFold(x2, CrcBlock(data, i + 16), CrcK1, CrcK2)
        x1 = CrcFold(x1, CrcBlock(data, i + 32), CrcK1, CrcK2)
        x0 = CrcFold(x0, CrcBlock(data, i + 48), CrcK1, CrcK2)
        i += 64
    }
    var x := CrcFold(CrcFold(CrcFold(x3, x2, CrcK3, CrcK4), x1, CrcK3, CrcK4), x0, CrcK3, CrcK4)
    for n - i >= 16 {
        x = CrcFold(x, CrcBlock(data, i), CrcK3, CrcK4)
        i += 16
    }
    *fold = x.Reinterpret[u8, 16]().ToArray()
    return n
}

/// Crc32Fold folds every whole 16-byte block of data, for CRC-32/ISO-HDLC
/// with the register c (pre-inverted, not the finished CRC), and reports
/// the bytes it consumed, data.Len() / 16 * 16: the CRC register from zero
/// over the 16 bytes it writes to fold equals c's over those bytes. It
/// reports 0, writing nothing, when data holds fewer than 64 bytes or the
/// CPU lacks pclmul and aes (always on x86_64-freestanding and at compile
/// time). O(n), two carry-less products per 16 bytes; it allocates
/// nothing.
export Crc32Fold(c: u32, data: []u8, fold: mut [16]u8): int {
    if Target.os == Os.Freestanding || data.Len() < 64 {
        return 0
    }
    id := simd.Cpu()
    if !id.Has("pclmul") || !id.Has("aes") {
        return 0
    }
    var k := 0
    unsafe "Cpu() reports pclmul and aes, which Crc32Ni's clause names" {
        k = Crc32Ni(c, data, fold)
    }
    return k
}

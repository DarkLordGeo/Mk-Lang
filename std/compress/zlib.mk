// Why: the zlib format (RFC 1950) wraps DEFLATE for HTTP's deflate coding
// and many file formats, and its Adler-32 must be fast on large inputs.
//
// What: compress.Adler32, Adler32State (its streaming form), compress.Zlib
// and compress.Unzlib: a 2-byte header, a raw DEFLATE stream and the
// Adler-32 of the data, big-endian.
//
// How: AdlerVec sums 32-byte blocks in SIMD lanes and reduces modulo
// AdlerMod only every AdlerVecBlocks blocks (the bound below shows no lane
// wraps before that); AdlerUpdate takes the last 0 to 31 bytes eight at a
// time, by one multiply of each word's 16-bit lanes.

import std.simd

// AdlerMod is the largest prime below 2^16 (RFC 1950 section 8.2).
const AdlerMod: u64 = 65521

// AdlerVecBlocks is how many 32-byte blocks AdlerVec sums between
// reductions. Per block a u32 lane of the byte sums gains at most 8 * 255
// = 2,040, so after j blocks it is below 2,040 j, and the prefix-sum lane,
// which gains the byte-sum lane each block, is below 1,020 j^2: 1.07e9 for
// j = 1,024, below 2^32. The weighted-sum lane gains at most 2 * 255 * (32
// + 24 + 16 + 8) = 40,800 a block (41.8e6 after 1,024 blocks), and each u16
// lane of a block's weighted sums at most 20,400 and of its byte sums 1,020,
// below 2^16, so no lane wraps (the wrapping operators only skip the check).
const AdlerVecBlocks = 1024

// The weights of a 32-byte block's bytes in s2: byte k counts 32 - k
// times.
const AdlerW0: [16]u8 = [32, 31, 30, 29, 28, 27, 26, 25, 24, 23, 22, 21, 20, 19, 18, 17]
const AdlerW1: [16]u8 = [16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1]

// The byte lanes of a little-endian word: bytes 0, 2, 4 and 6 in 16-bit
// lanes, and the multipliers that leave in the top lane the lanes' sum
// and their sums weighted 8, 6, 4, 2 (even bytes) and 7, 5, 3, 1 (odd).
const LaneMask: u64 = 0x00FF00FF00FF00FF
const LaneSum: u64 = 0x0001000100010001
const LaneEven: u64 = 0x0008000600040002
const LaneOdd: u64 = 0x0007000500030001

// AdlerVec is (s1, s2), each below AdlerMod on entry, after the nb (at
// most AdlerVecBlocks) 32-byte blocks of data from i, reduced. Over a block
// of bytes b0 to b31, s2 gains 32 s1 + 32 b0 + 31 b1 + ... + 1 b31 and s1
// gains the bytes' sum; across nb blocks s2 gains 32 nb s1, 32 times the
// sum of every block's byte sums before it (the prefix lanes vp, which gain
// the byte-sum lanes vs before each block) and the blocks' weighted sums
// (vw). Sixteen bytes a vector at the hosted baseline (x86-64-v2), with no
// run-time choice: the method of Chromium's adler32_simd.c (as zlib-ng's
// adler32_ssse3), the weighted sums by widening multiplies.
AdlerVec(s1: u64, s2: u64, data: []u8, i: int, nb: int): (s1: u64, s2: u64) {
    w0 := simd.Vec[u8, 16].FromArray(AdlerW0)
    w1 := simd.Vec[u8, 16].FromArray(AdlerW1)
    var vs := simd.Vec[u32, 4].Zero()
    var vp := simd.Vec[u32, 4].Zero()
    var vw := simd.Vec[u32, 4].Zero()
    var p := i
    for _ in 0..nb {
        a := simd.Vec[u8, 16].FromSlice(data, p)
        b := simd.Vec[u8, 16].FromSlice(data, p + 16)
        vp = vp +% vs
        sums := a.WidenLo[u16, 8]() +% a.WidenHi[u16, 8]() +% b.WidenLo[u16, 8]() +% b.WidenHi[u16, 8]()
        ws := a.MulWidenLo[u16, 8](w0) +% a.MulWidenHi[u16, 8](w0) +% b.MulWidenLo[u16, 8](w1) +% b.MulWidenHi[u16, 8](w1)
        vs = vs +% sums.WidenLo[u32, 4]() +% sums.WidenHi[u32, 4]()
        vw = vw +% ws.WidenLo[u32, 4]() +% ws.WidenHi[u32, 4]()
        p += 32
    }
    t1 := s1 + Cast[u64](vs.ReduceSumWide())
    t2 := s2 + 32 * Cast[u64](nb) * s1 + 32 * Cast[u64](vp.ReduceSumWide()) + Cast[u64](vw.ReduceSumWide())
    return (s1: t1 % AdlerMod, s2: t2 % AdlerMod)
}

// AdlerUpdate is the Adler-32 state s (s2 << 16 | s1) after data: whole
// 32-byte blocks by AdlerVec, reduced every AdlerVecBlocks blocks (zlib's
// NMAX, 5,552 bytes, is the bound for scalar u32 sums); the last 0 to 31
// bytes eight a step, each word's byte sum and weighted sums by one
// multiply of its 16-bit lanes (every lane product below 2^16, so no carry
// crosses a lane), then a byte at a time.
AdlerUpdate(s: u32, data: []u8): u32 {
    var s1 := u64(s & 0xFFFF)
    var s2 := u64(s >> 16)
    n := data.Len()
    var i := 0
    for n - i >= 32 {
        var nb := (n - i) / 32
        if nb > AdlerVecBlocks {
            nb = AdlerVecBlocks
        }
        r := AdlerVec(s1, s2, data, i, nb)
        s1 = r.s1
        s2 = r.s2
        i += 32 * nb
    }
    for i + 8 <= n {
        w := Le64(data, i)
        ev := w & LaneMask
        od := (w >> 8) & LaneMask
        sum := ((ev + od) *% LaneSum) >> 48
        wsum := ((ev *% LaneEven) >> 48) + ((od *% LaneOdd) >> 48)
        s2 += (s1 << 3) + wsum
        s1 += sum
        i += 8
    }
    for i < n {
        s1 += u64(data[i])
        s2 += s1
        i += 1
    }
    return Trunc[u32](((s2 % AdlerMod) << 16) | (s1 % AdlerMod))
}

/// The Adler-32 checksum of data, the checksum of the zlib format (RFC
/// 1950); 1 for no data. O(n); allocates nothing.
export Adler32(data: []u8): u32 {
    return AdlerUpdate(1, data)
}

/// An Adler-32 in progress over the bytes so far, which streams Adler32 as
/// hash.Crc32State streams hash.Crc32. It holds the checksum xor 1, so its
/// zero value is the state of the empty input, whose checksum is 1: a
/// zero-filled Adler32State is a fresh one, and the type is zero-valid.
export struct Adler32State {
    x: u32
}

/// The state of the empty input. O(1); allocates nothing.
export Adler32State.New(): Adler32State {
    return Adler32State(x: 0)
}

/// Absorbs data. Feeding the input in any split gives the Adler32 of the
/// whole. O(data.Len()); allocates nothing and copies nothing.
export Adler32State.Bytes(mut self, data: []u8) {
    self.x = AdlerUpdate(self.x ^ 1, data) ^ 1
}

/// The Adler-32 of everything absorbed. It takes the state by value, so
/// the state can keep absorbing. O(1); allocates nothing.
export Adler32State.Sum(self): u32 {
    return self.x ^ 1
}

// PutBe32 stores x at out[i..i + 4], big-endian (RFC 1950's byte order).
PutBe32(out: mut []u8, i: int, x: u32) {
    out[i] = Trunc[u8](x >> 24)
    out[i + 1] = Trunc[u8](x >> 16)
    out[i + 2] = Trunc[u8](x >> 8)
    out[i + 3] = Trunc[u8](x)
}

// Be32 is the 4 bytes of b at i as a big-endian word.
Be32(b: []u8, i: int): u32 {
    w := b[i..b.Len()][0..4]
    return (u32(w[0]) << 24) | (u32(w[1]) << 16) | (u32(w[2]) << 8) | u32(w[3])
}

// ZlibFlg is the FLG byte after CMF 0x78 (deflate, a 32 KiB window): FLEVEL
// 0 (fastest) for stored output and 2 (default) for the one compressing
// level, as Go's compress/zlib writes them, and FCHECK making the pair a
// multiple of 31 (RFC 1950 section 2.2).
ZlibFlg(level: int): u8 {
    return if level == 0 { 0x01 } else { 0x9C }
}

// ZlibCmfBad is whether CMF names no stream this decoder reads: a method
// other than 8 (deflate), or a window above 32 KiB (CINFO above 7).
ZlibCmfBad(cmf: u8): bool {
    return cmf & 0x0F != 8 || cmf >> 4 > 7
}

// ZlibFlgBad is whether FLG is wrong after cmf: the pair not a multiple of
// 31, or FDICT set (no preset dictionary is supported, as in Go's reader
// without one).
ZlibFlgBad(cmf: u8, flg: u8): bool {
    return ((int(cmf) << 8) | int(flg)) % 31 != 0 || flg & 0x20 != 0
}

// ZlibHeader checks the zlib header at data[0..2] and returns the offset
// of its deflate stream.
ZlibHeader(data: []u8): Error!int {
    n := data.Len()
    if n < 1 {
        fail Truncated
    }
    if ZlibCmfBad(data[0]) {
        fail Corrupt(offset: 0)
    }
    if n < 2 {
        fail Truncated
    }
    if ZlibFlgBad(data[0], data[1]) {
        fail Corrupt(offset: 1)
    }
    return 2
}

/// Compresses data as one zlib stream (RFC 1950): the header 0x78 0x9C,
/// Deflate's stream, then the Adler-32 of data.
///
/// Fails only with OutOfMemory. O(n); allocates Deflate's bound plus 6
/// bytes in a for the output, and 448 KiB of scratch in a sub-arena of a,
/// freed before it returns.
export Zlib(a: &Arena, data: []u8): OutOfMemory![]u8 from a {
    out := try a.Slice[u8](DeflateBound(data.Len()) + 6)
    out[0] = 0x78
    out[1] = ZlibFlg(6)
    q := try DeflateInto(a, data, out, 2)
    PutBe32(out, q, Adler32(data))
    return out[0..q + 4]
}

/// Decodes a zlib stream (RFC 1950) into at most limit bytes. All of data
/// is the one stream; a negative limit acts as 0.
///
/// Fails with Truncated when data ends inside the stream, and TooLarge
/// when the output would pass limit. Fails with Corrupt at a bad header
/// byte (a method other than deflate, a window over 32 KiB, a bad check,
/// a preset dictionary), at the first bad element of the deflate stream,
/// at an Adler-32 that differs from the output's, and at a byte after the
/// stream. O(data + output); allocates the output in a as Inflate does.
export Unzlib(a: &Arena, data: []u8, limit: int): Error![]u8 from a {
    lim := Lim(limit)
    n := data.Len()
    q := try ZlibHeader(data)
    buf := try a.Slice[u8](FirstCap(n, lim))
    var d := NewDec(q, 0)
    out := try Drive(a, mut d, data, buf, lim)
    end := StreamEnd(&d.c)
    if n - end < 4 {
        fail Truncated
    }
    op := d.c.op
    if Be32(data, end) != Adler32(out[0..op]) {
        fail Corrupt(offset: end)
    }
    if end + 4 != n {
        fail Corrupt(offset: end + 4)
    }
    return out[0..op]
}

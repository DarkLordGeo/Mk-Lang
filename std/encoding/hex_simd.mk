// Why: hex of large inputs is limited by one table load per byte; vector
// shuffles do 16 bytes a step at the hosted baseline.
//
// What: HexEncodeVec and HexDecodeVec, the vector paths of hex.mk.
//
// How: the method of Muła's "SIMD-ized hex encoding and decoding" and of
// Lemire's hex decoder. Encoding splits each byte into its two nibbles,
// maps both through one 16-entry table (Lookup16, pshufb) and interleaves
// them; decoding classifies 32 characters as digits or letters of either
// case by two wrapping subtractions, refuses the block if any lane is
// neither, and packs each pair of nibbles into a byte. A decode step that
// meets a byte that is not a hex digit stops, and the scalar loop goes on
// from that step and names the byte, so errors are the scalar path's.

import std.simd

const HexDigitsArray: [16]u8 = [0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66]

// HexEncodeVec writes the hex of whole 16-byte blocks of data to dst, which
// holds 2 * data.Len() bytes, and returns how many bytes of data it took.
HexEncodeVec(dst: mut []u8, data: []u8): int {
    digits := simd.Vec[u8, 16].FromArray(HexDigitsArray)
    nibble := simd.Vec[u8, 16].Splat(15)
    var i := 0
    for data.Len() - i >= 16 {
        v := simd.Vec[u8, 16].FromSlice(data, i)
        hi := simd.Lookup16[16](v >> 4, digits)
        lo := simd.Lookup16[16](v & nibble, digits)
        hi.InterleaveLo(lo).CopyTo(dst, 2 * i)
        hi.InterleaveHi(lo).CopyTo(dst, 2 * i + 16)
        i += 16
    }
    return i
}

// HexNibbles is the value of each lane of c as a hex digit, and whether every
// lane is one.
HexNibbles(c: simd.Vec[u8, 16]): (values: simd.Vec[u8, 16], ok: bool) {
    digit := c -% simd.Vec[u8, 16].Splat(0x30)
    letter := (c | simd.Vec[u8, 16].Splat(0x20)) -% simd.Vec[u8, 16].Splat(0x61)
    is_digit := digit < simd.Vec[u8, 16].Splat(10)
    is_letter := letter < simd.Vec[u8, 16].Splat(6)
    values := is_digit.Select(digit, letter +% simd.Vec[u8, 16].Splat(10))
    return (values, ok: (is_digit | is_letter).All())
}

// HexDecodeVec decodes whole 32-character blocks of b, each to 16 bytes,
// while every byte of a block is a hex digit and 16 bytes of dst remain,
// and returns how many bytes it wrote; the scalar loop finishes the rest
// and finds the first bad byte itself.
HexDecodeVec(dst: mut []u8, b: []u8): int {
    low_byte := simd.Vec[u16, 8].Splat(0xFF)
    var o := 0
    for b.Len() - 2 * o >= 32 && dst.Len() - o >= 16 {
        first := HexNibbles(simd.Vec[u8, 16].FromSlice(b, 2 * o))
        second := HexNibbles(simd.Vec[u8, 16].FromSlice(b, 2 * o + 16))
        if !first.ok || !second.ok {
            break
        }
        // Each 16-bit lane holds a pair, high nibble first in memory: the
        // byte is low << 4 | high, kept in the lane's even byte.
        w0 := first.values.Reinterpret[u16, 8]()
        w1 := second.values.Reinterpret[u16, 8]()
        p0 := (((w0 & low_byte) << 4) | (w0 >> 8)).Reinterpret[u8, 16]()
        p1 := (((w1 & low_byte) << 4) | (w1 >> 8)).Reinterpret[u8, 16]()
        p0.DeinterleaveEven(p1).CopyTo(dst, o)
        o += 16
    }
    return o
}

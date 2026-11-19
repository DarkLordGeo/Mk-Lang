// Why: base64 of large inputs is limited by the scalar loop's one table
// load per character; byte shuffles do 16 characters at once.
//
// What: Base64EncodeVec and Base64DecodeVec, the vector paths of base64.mk,
// with the compile-time tables EncShift, DecLo, DecHi and DecRoll for the
// standard and URL alphabets.
//
// How: Muła and Lemire's method ("Faster Base64 Encoding and Decoding Using
// AVX2 Instructions", ACM TOMS 44(3), 2018) in its 16-byte SSSE3 form,
// where std.simd's Lookup16 is pshufb. Encoding takes 12 bytes to 16
// characters a step: a shuffle spreads each three bytes over a 32-bit lane,
// two multiplies on 16-bit lanes move the four 6-bit fields to the four
// bytes, and one lookup on a reduced index adds each field's offset to its
// character. Decoding takes 16 characters to 12 bytes: two lookups by
// nibble classify each byte (a byte is in the alphabet exactly when its two
// class sets share no bit), one lookup by high nibble gives the offset from
// character to value, and shifts pack four 6-bit values into three bytes.
// A decode step that meets a byte outside the alphabet stops, and the
// scalar loop goes on from there and names the byte, so errors and their
// offsets are the scalar path's.

import std.simd

type V = simd.Vec[u8, 16]

// EncShift is Muła's offset table for an alphabet whose characters 62 and
// 63 are c62 and c63, indexed by the reduced field (Lookup's
// "pshufb improved"): 13 for 0 to 25, 0 for 26 to 51, 1 to 10 for 52 to
// 61, 11 and 12 for 62 and 63.
EncShift(c62: u8, c63: u8): [16]u8 {
    var t: [16]u8 = [0; 16]
    t[0] = 0x61 - 26
    for k in 1..11 {
        t[k] = Trunc[u8](0x30 - 52)
    }
    t[11] = Trunc[u8](int(c62) - 62)
    t[12] = Trunc[u8](int(c63) - 63)
    t[13] = 0x41
    return t
}

const StdShift: [16]u8 = EncShift(b'+', b'/')
const UrlShift: [16]u8 = EncShift(b'-', b'_')

// Base64EncodeVec encodes whole 12-byte blocks of data into dst while 16
// bytes of data and of dst remain, and returns how many bytes it read and
// wrote; the scalar loop finishes the rest.
Base64EncodeVec(dst: mut []u8, data: []u8, url: bool): (read: int, wrote: int) {
    shift := V.FromArray(if url { UrlShift } else { StdShift })
    keep_hi := simd.Vec[u32, 4].Splat(0x0FC0FC00)
    keep_lo := simd.Vec[u32, 4].Splat(0x003F03F0)
    mul_hi := simd.Vec[u32, 4].Splat(0x04000040).Reinterpret[u16, 8]()
    mul_lo := simd.Vec[u32, 4].Splat(0x01000010).Reinterpret[u16, 8]()
    s51 := V.Splat(51)
    s26 := V.Splat(26)
    s13 := V.Splat(13)
    zero := V.Zero()
    n := data.Len()
    var i := 0
    var o := 0
    for n - i >= 16 && dst.Len() - o >= 16 {
        // Bytes a b c to the 32-bit lane b a c b, little-endian.
        v := V.FromSlice(data, i).Shuffle(zero, [1, 0, 2, 1, 4, 3, 5, 4, 7, 6, 8, 7, 10, 9, 11, 10])
        w := v.Reinterpret[u32, 4]()
        t1 := (w & keep_hi).Reinterpret[u16, 8]().MulHigh(mul_hi)
        t3 := (w & keep_lo).Reinterpret[u16, 8]() *% mul_lo
        idx := (t1 | t3).Reinterpret[u8, 16]()
        reduced := idx.SubSat(s51) | (idx < s26).Select(s13, zero)
        out := simd.Lookup16[16](reduced, shift) +% idx
        out.CopyTo(dst, o)
        i += 12
        o += 16
    }
    return (read: i, wrote: o)
}

// ValidSets is, for each high nibble, the set of low nibbles that make a
// character of chars, as a 16-bit mask.
ValidSets(chars: str): [16]u32 {
    var valid: [16]u32 = [0; 16]
    for i in 0..64 {
        c := chars.Byte(i)
        valid[int(c >> 4)] = valid[int(c >> 4)] | (u32(1) << u32(c & 15))
    }
    return valid
}

// ClassOf numbers the distinct sets of valid: the class of high nibble h is
// the index of the first nibble whose set equals h's. An alphabet of 64
// ASCII characters has at most 8 distinct sets.
ClassOf(valid: &[16]u32, h: int): int {
    var k := 0
    for valid[k] != valid[h] {
        k += 1
    }
    return k
}

// DecHi is Muła's high-nibble table: one bit per class, the class's bit.
// Classes are numbered by ClassRank, so eight bits hold them.
DecHi(chars: str): [16]u8 {
    valid := ValidSets(chars)
    var t: [16]u8 = [0; 16]
    for h in 0..16 {
        t[h] = Trunc[u8](u32(1) << Trunc[u32](ClassRank(&valid, ClassOf(&valid, h))))
    }
    return t
}

// ClassRank is the rank of class k (a first nibble) among the classes.
ClassRank(valid: &[16]u32, k: int): int {
    var r := 0
    for j in 0..k {
        if ClassOf(valid, j) == j {
            r += 1
        }
    }
    return r
}

// DecLo is Muła's low-nibble table: lane l holds the bit of every class in
// which low nibble l makes no character of chars.
DecLo(chars: str): [16]u8 {
    valid := ValidSets(chars)
    var t: [16]u8 = [0; 16]
    for l in 0..16 {
        var bits: u32 = 0
        for h in 0..16 {
            if valid[h] & (u32(1) << Trunc[u32](l)) == 0 {
                bits = bits | (u32(1) << Trunc[u32](ClassRank(&valid, ClassOf(&valid, h))))
            }
        }
        t[l] = Trunc[u8](bits)
    }
    return t
}

// DecRoll is the table of offsets from character to value by high nibble;
// the character special, which shares its nibble with characters of
// another offset, is moved to slot (its nibble + adjust) mod 256.
DecRoll(chars: str, special: u8, adjust: u8): [16]u8 {
    var t: [16]u8 = [0; 16]
    for i in 0..64 {
        c := chars.Byte(i)
        slot := if c == special { (int(c >> 4) + int(adjust)) & 0xFF } else { int(c >> 4) }
        t[slot] = Trunc[u8](i - int(c))
    }
    return t
}

const StdLo: [16]u8 = DecLo(StdChars)
const StdHi: [16]u8 = DecHi(StdChars)
const UrlLo: [16]u8 = DecLo(UrlChars)
const UrlHi: [16]u8 = DecHi(UrlChars)
const StdRoll: [16]u8 = DecRoll(StdChars, b'/', 0xFF)
const UrlRoll: [16]u8 = DecRoll(UrlChars, b'_', 0xFC)

// Base64DecodeVec decodes whole 16-character blocks of b, each to 12
// bytes, while every character of a block is in the alphabet and 16 bytes
// of dst remain, and returns how many characters it read and bytes it
// wrote; the scalar loop finishes the rest and finds the first bad
// character itself.
Base64DecodeVec(dst: mut []u8, b: []u8, url: bool): (read: int, wrote: int) {
    lo_table := V.FromArray(if url { UrlLo } else { StdLo })
    hi_table := V.FromArray(if url { UrlHi } else { StdHi })
    roll := V.FromArray(if url { UrlRoll } else { StdRoll })
    special := V.Splat(if url { b'_' } else { b'/' })
    adjust := V.Splat(if url { 0xFC } else { 0xFF })
    nibble := V.Splat(15)
    zero := V.Zero()
    low_byte := simd.Vec[u16, 8].Splat(0xFF)
    low_half := simd.Vec[u32, 4].Splat(0xFFFF)
    var i := 0
    var o := 0
    for b.Len() - i >= 16 && dst.Len() - o >= 16 {
        v := V.FromSlice(b, i)
        hi := v >> 4
        bad := simd.Lookup16[16](v & nibble, lo_table) & simd.Lookup16[16](hi, hi_table)
        if bad.ReduceOr() != 0 {
            break
        }
        slot := hi +% (v == special).Select(adjust, zero)
        values := v +% simd.Lookup16[16](slot, roll)
        // Pairs of 6-bit values to 12 bits, pairs of those to 24, then the
        // three bytes of each 32-bit lane, most significant first.
        w := values.Reinterpret[u16, 8]()
        twelve := ((w & low_byte) << 6) | (w >> 8)
        x := twelve.Reinterpret[u32, 4]()
        packed := ((x & low_half) << 12) | (x >> 16)
        out := packed.Reinterpret[u8, 16]().Shuffle(zero, [2, 1, 0, 6, 5, 4, 10, 9, 8, 14, 13, 12, 3, 7, 11, 15])
        out.CopyTo(dst, o)
        i += 16
        o += 12
    }
    return (read: i, wrote: o)
}

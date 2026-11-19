// Why: legacy single-byte encodings (windows-1252, KOI8-R and the others)
// are a large share of old text, and decoding them a byte at a time
// through a branchy loop is slow.
//
// What: SingleCount and SingleRun, the single-byte half of text.mk's Run:
// the WHATWG single-byte decoder over an index, where 0 is a byte the
// index leaves out.
//
// How: from 64 bytes the count is vectorised (SingleCount), and the write
// first packs each high byte's UTF-8 form and length into a table indexed
// by the byte (the per-byte table of encoding_rs and Go's x/text charmap,
// built here from the index so a program links only the indexes it
// names); it copies an ASCII block of 16 bytes as a vector (one compare)
// and a high byte from its word, U+FFFD for a byte the index leaves out. A
// branch-free form (a four-byte store per byte, advancing by the word's
// length) was measured slower on the encoding-single bench line.

import std.simd

// SingleFastMin is the length from which building the table pays.
const SingleFastMin: int = 64

// Packed is code point m (U+0080 to U+FFFF) as UTF-8, first byte lowest,
// with its length in the top byte.
Packed(m: u32): u32 {
    if m < 0x800 {
        return 0x02000000 | ((0x80 | (m & 0x3F)) << 8) | (0xC0 | (m >> 6))
    }
    return 0x03000000 | ((0x80 | (m & 0x3F)) << 16) | ((0x80 | ((m >> 6) & 0x3F)) << 8) | (0xE0 | (m >> 12))
}

// SingleRun is Run for a single-byte encoding with index t.
SingleRun(t: []u16, b: []u8, stop: int, out: mut []u8, write: bool, strict: bool): Ran {
    if stop < SingleFastMin {
        return SingleBytes(t, b, stop, out, write, strict)
    }
    if !write {
        return SingleCount(t, b, stop, out, strict)
    }
    // Each high byte's UTF-8 form, U+FFFD for a byte the index leaves out:
    // the counting pass before this one has reported it, and a strict
    // caller writes only after a count that found none.
    var enc: [256]u32 = [0; 256]
    for k in 0..128 {
        m := u32(t[k])
        enc[k + 128] = Packed(if m == 0 { 0xFFFD } else { m })
    }
    high := simd.Vec[u8, 16].Splat(0x80)
    var i := 0
    var o := 0
    for i < stop {
        var end := stop
        if stop - i >= 16 {
            v := simd.Vec[u8, 16].FromSlice(b, i)
            if !(v >= high).Any() {
                v.CopyTo(out, o)
                i += 16
                o += 16
                continue
            }
            end = i + 16
        }
        for x in b[i..end] {
            if x < 0x80 {
                out[o] = x
                o += 1
                continue
            }
            e := enc[int(x)]
            if e < 0x03000000 {
                var d := mut out[o..o + 2]
                d[0] = Trunc[u8](e)
                d[1] = Trunc[u8](e >> 8)
                o += 2
            } else {
                var d := mut out[o..o + 3]
                d[0] = Trunc[u8](e)
                d[1] = Trunc[u8](e >> 8)
                d[2] = Trunc[u8](e >> 16)
                o += 3
            }
        }
        i = end
    }
    return (read: stop, wrote: o, bad: -1)
}

// SingleCount is SingleRun's counting pass. A high byte writes two bytes,
// or three when its code point is U+0800 or above; which high bytes those
// are, and which the index leaves out, are two 128-bit maps, so sixteen
// bytes are counted by two nibble lookups (pshufb: the map's byte by the
// high byte's top four bits, the bit by its low three) and two mask
// counts. An input holding a byte the index leaves out is counted by the
// byte loop, which finds the first one.
SingleCount(t: []u16, b: []u8, stop: int, out: mut []u8, strict: bool): Ran {
    var wide: [16]u8 = [0; 16]
    var gap: [16]u8 = [0; 16]
    var gaps := false
    for k in 0..128 {
        bit := u8(1) << Trunc[u8](k & 7)
        if t[k] == 0 {
            gap[k >> 3] |= bit
            gaps = true
        } else if t[k] >= 0x800 {
            wide[k >> 3] |= bit
        }
    }
    wide_map := simd.Vec[u8, 16].FromArray(wide)
    gap_map := simd.Vec[u8, 16].FromArray(gap)
    bits := simd.Vec[u8, 16].FromArray([1, 2, 4, 8, 16, 32, 64, 128, 1, 2, 4, 8, 16, 32, 64, 128])
    high := simd.Vec[u8, 16].Splat(0x80)
    seven := simd.Vec[u8, 16].Splat(7)
    zero := simd.Vec[u8, 16].Zero()
    var o := stop
    var i := 0
    var holes := false
    for stop - i >= 16 {
        v := simd.Vec[u8, 16].FromSlice(b, i)
        is_high := v >= high
        if is_high.Any() {
            // An ASCII byte's index is 16 or more, which looks up 0.
            idx := (v ^ high) >> 3
            bit := simd.Lookup16[16](v & seven, bits)
            o += is_high.CountSet() + ((simd.Lookup16[16](idx, wide_map) & bit) != zero).CountSet()
            if gaps && ((simd.Lookup16[16](idx, gap_map) & bit) != zero).Any() {
                holes = true
            }
        }
        i += 16
    }
    for x in b[i..stop] {
        if x >= 0x80 {
            m := t[int(x) - 0x80]
            holes = holes || m == 0
            o += if m >= 0x800 { 2 } else { 1 }
        }
    }
    if holes {
        return SingleBytes(t, b, stop, out, false, strict)
    }
    return (read: stop, wrote: o, bad: -1)
}

// SingleBytes is the byte loop of a single-byte encoding with index t.
SingleBytes(t: []u16, b: []u8, stop: int, out: mut []u8, write: bool, strict: bool): Ran {
    var i := 0
    var o := 0
    var bad := -1
    for i < stop {
        x := b[i]
        var c := u32(x)
        if x >= 0x80 {
            m := t[int(x) - 0x80]
            c = if m == 0 { Bad } else { u32(m) }
        }
        if c == Bad {
            if bad < 0 {
                bad = i
            }
            if strict {
                break
            }
        }
        o = if write { Put(out, o, c) } else { o + Utf8Len(c) }
        i += 1
    }
    return (read: i, wrote: o, bad)
}

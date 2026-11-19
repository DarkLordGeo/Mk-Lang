// strscan, Mk: one std.simd string kernel per variant (README.md) over a
// text of <size> bytes built from splitmix64 words, reps calls, call r on
// the text from offset r mod 64 (the first 128 bytes are ASCII, so every
// offset starts a scalar): simd.IsAscii, simd.LastIndexByte (the one 0xFF
// at byte 100, so the scan runs from the end to it), simd.Utf16Len and
// simd.ValidWtf8 (a text with lone surrogates), each dispatched at run time
// to its avx512bw, avx2 or 16-lane form (std/simd/scan.mk). The checksum
// adds each answer (a bool as 0 or 1) and the offset.
// Usage: strscan <variant> <size> <reps> <seed>.

import std.simd

Mix(z0: u64): u64 {
    var z := z0
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

// Fill writes the variant's text: 128 ASCII letters, then pieces chosen by
// z mod 8 (five ASCII letters in eight, else é, € or 😀, and for wtf one
// 😀 in four a lone surrogate ED A0 80); ascii keeps only the letters. A
// piece that does not fit is replaced by letters.
Fill(buf: mut []u8, ascii: bool, wtf: bool, seed: u64) {
    n := buf.Len()
    var st := seed
    var i := 0
    for i < n && i < 128 {
        buf[i] = 0x61 + Trunc[u8](i % 26)
        i += 1
    }
    for i < n {
        st = st +% 0x9E3779B97F4A7C15
        z := Mix(st)
        p := if ascii { 0 } else { z % 8 }
        if p < 5 || i + 4 > n {
            buf[i] = 0x61 + Trunc[u8]((z >> 8) % 26)
            i += 1
        } else if p == 5 {
            buf[i] = 0xC3
            buf[i + 1] = 0xA9
            i += 2
        } else if p == 6 {
            buf[i] = 0xE2
            buf[i + 1] = 0x82
            buf[i + 2] = 0xAC
            i += 3
        } else if wtf && (z >> 16) % 4 == 0 {
            buf[i] = 0xED
            buf[i + 1] = 0xA0
            buf[i + 2] = 0x80
            i += 3
        } else {
            buf[i] = 0xF0
            buf[i + 1] = 0x9F
            buf[i + 2] = 0x98
            buf[i + 3] = 0x80
            i += 4
        }
    }
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError|Utf8Error!void {
    name := sys.args[1]
    n := Cast[int](try sys.args[2].ParseInt())
    reps := Cast[int](try sys.args[3].ParseInt())
    seed := Cast[u64](try sys.args[4].ParseInt())
    buf := try sys.arena.Slice[u8](n)
    ascii := name == "isascii" || name == "lastindexbyte"
    Fill(buf, ascii, name == "validwtf8", seed)
    if name == "lastindexbyte" {
        buf[100] = 0xFF
    }
    b := buf[..]
    var acc := 0
    if name == "isascii" {
        for r in 0..reps {
            off := r % 64
            k := if simd.IsAscii(b[off..]) { 1 } else { 0 }
            acc = acc +% k +% off
        }
    } else if name == "lastindexbyte" {
        for r in 0..reps {
            off := r % 64
            k := simd.LastIndexByte(b[off..], 0xFF) else { -1 }
            acc = acc +% k +% off
        }
    } else if name == "utf16len" {
        s := try str.From(b)
        for r in 0..reps {
            off := r % 64
            acc = acc +% simd.Utf16Len(s[off..]) +% off
        }
    } else {
        for r in 0..reps {
            off := r % 64
            k := if simd.ValidWtf8(b[off..]) { 1 } else { 0 }
            acc = acc +% k +% off
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{acc}\n", (acc))
}

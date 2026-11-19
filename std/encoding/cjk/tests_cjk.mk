// Why: each CJK handler has edge cases (restored bytes, modes, units cut
// between chunks) that the exported calls must show one at a time.
// What: test blocks of std.encoding.cjk, one behaviour each, with the
// helpers EncodeFails and Why.
// How: the differential tests over every pointer and scalar, the
// web-platform-tests cases and the fuzz target are std/std-encodingcjk_test.go.

import std.encoding
import std.simd

// EncodeFails is whether e has no bytes for s.
EncodeFails(a: &Arena, e: encoding.Encoding, s: str): bool {
    _ = e.Encode(a, s) else {
        return true
    }
    return false
}

// Why is the member Lookup of label failed with, "" on success.
Why(label: str): str {
    _ = Lookup(label) else e {
        return match e {
            encoding.UnknownLabel { "UnknownLabel" }
        }
    }
    return ""
}

test "Lookup names every label of the standard, the CJK ones here and the rest through encoding.Lookup" (sys: &System) {
    Assert(Lookup("shift_jis").Must().Name() == "Shift_JIS")
    Assert(Lookup(" Windows-31J\n").Must().Name() == "Shift_JIS")
    Assert(Lookup("GB2312").Must().Name() == "GBK")
    Assert(Lookup("gb18030").Must().Name() == "gb18030")
    Assert(Lookup("big5-hkscs").Must().Name() == "Big5")
    Assert(Lookup("x-euc-jp").Must().Name() == "EUC-JP")
    Assert(Lookup("csISO2022JP").Must().Name() == "ISO-2022-JP")
    Assert(Lookup("windows-949").Must().Name() == "EUC-KR")
    Assert(Lookup("latin1").Must().Name() == "windows-1252")
    Assert(Lookup("utf8").Must().Name() == "UTF-8")
    Assert(Why("iso-2022-kr") == "")
    Assert(Lookup("iso-2022-kr").Must().Name() == "replacement")
    Assert(Why("shift_jis2") == "UnknownLabel")
    Assert(Why("") == "UnknownLabel")
    // encoding.Lookup itself still answers Unsupported for them.
    _ = encoding.Lookup("shift_jis") else e {
        Assert(match e {
            encoding.UnknownLabel { false }
            encoding.Unsupported { true }
        })
        return
    }
    Assert(false)
}

const SjText: [8]u8 = [0x82, 0xA0, 0x84, 0x45, 0xA1, 0xF0, 0x40, 0x80]

test "Shift_JIS decodes two-byte, halfwidth katakana, EUDC and 0x80 units, and a two-byte unit that is two bytes of UTF-8 too" (sys: &System) {
    a := sys.arena
    // 0x84 0x45 is U+0415, two bytes in UTF-8 as in Shift_JIS.
    Assert(ShiftJis.Decode(a, SjText[..]).Must() == "あ\u{415}\u{FF61}\u{E000}\u{80}")
    Assert(ShiftJis.DecodeStrict(a, SjText[2..4]).Must() == "\u{415}")
    Assert(ShiftJis.Decode(a, b"\x84\x45\x84\x46").Must() == "\u{415}\u{401}")
}

test "Shift_JIS: a lead byte before ASCII is one U+FFFD and the ASCII is read again; a lead at the end is one U+FFFD" (sys: &System) {
    a := sys.arena
    Assert(ShiftJis.Decode(a, b"\x82A").Must() == "\u{FFFD}A")
    Assert(ShiftJis.Decode(a, b"\x82\xFD").Must() == "\u{FFFD}")
    Assert(ShiftJis.Decode(a, b"a\x82").Must() == "a\u{FFFD}")
    _ = ShiftJis.DecodeStrict(a, b"a\x82") else e {
        Assert(match e {
            encoding.BadInput(offset) { offset == 1 }
            OutOfMemory { false }
        })
        return
    }
    Assert(false)
}

test "Shift_JIS encodes ASCII, U+00A5, halfwidth katakana and U+2212, and fails at the first scalar it has no bytes for" (sys: &System) {
    a := sys.arena
    Assert(simd.Equal(ShiftJis.Encode(a, "aあ¥\u{FF61}\u{2212}").Must(), b"a\x82\xA0\x5C\xA1\x81\x7C"))
    _ = ShiftJis.Encode(a, "ab\u{E000}") else e {
        Assert(match e {
            encoding.BadInput(offset) { offset == 2 }
            encoding.Unsupported { false }
            OutOfMemory { false }
        })
        return
    }
    Assert(false)
}

test "EUC-JP decodes JIS X 0212 after 0x8F and katakana after 0x8E, and encodes neither JIS X 0212 nor EUDC" (sys: &System) {
    a := sys.arena
    Assert(EucJp.Decode(a, b"\x8F\xA2\xAF\x8E\xB1\xA4\xA2").Must() == "\u{2D8}\u{FF71}あ")
    Assert(simd.Equal(EucJp.Encode(a, "\u{FF71}あ¥").Must(), b"\x8E\xB1\xA4\xA2\x5C"))
    Assert(EncodeFails(a, EucJp, "\u{2D8}"))
    Assert(EucJp.Decode(a, b"\x8F\xA2A").Must() == "\u{FFFD}A")
}

test "ISO-2022-JP changes mode on an escape, and a second escape with nothing between is U+FFFD" (sys: &System) {
    a := sys.arena
    Assert(Iso2022Jp.Decode(a, b"a\x1B$B\x24\x22\x1B(J\x5C\x1B(I\x31\x1B(Bb").Must() == "aあ¥\u{FF71}b")
    Assert(Iso2022Jp.Decode(a, b"\x1B(B\x1B(Bx").Must() == "\u{FFFD}x")
    Assert(Iso2022Jp.Decode(a, b"\x1B$x").Must() == "\u{FFFD}$x")
    Assert(Iso2022Jp.Decode(a, b"\x1B(").Must() == "\u{FFFD}(")
    Assert(Iso2022Jp.Decode(a, b"\x0E").Must() == "\u{FFFD}")
}

test "an ISO-2022-JP Decoder keeps its mode between chunks and holds an escape cut between them" (sys: &System) {
    a := sys.arena
    var d := Iso2022Jp.Decoder()
    var t := Text.New(a)
    t.Push(d.Push(a, b"\x1B$", false).Must()).Must()
    t.Push(d.Push(a, b"B\x24", false).Must()).Must()
    t.Push(d.Push(a, b"\x22\x24\x24", false).Must()).Must()
    t.Push(d.Push(a, b"\x1B(Bz", true).Must()).Must()
    Assert(t.Str() == "あいz")
    // After last the decoder starts again in ASCII.
    Assert(d.Push(a, b"\x24\x22", true).Must() == "$\"")
}

test "ISO-2022-JP encodes with escapes, ends in ASCII, and fails at SO, SI and ESC" (sys: &System) {
    a := sys.arena
    Assert(simd.Equal(Iso2022Jp.Encode(a, "a¥あ\u{FF71}b").Must(), b"a\x1B(J\x5C\x1B$B\x24\x22\x25\x22\x1B(Bb"))
    Assert(simd.Equal(Iso2022Jp.Encode(a, "あ").Must(), b"\x1B$B\x24\x22\x1B(B"))
    Assert(simd.Equal(Iso2022Jp.Encode(a, "ab").Must(), b"ab"))
    _ = Iso2022Jp.Encode(a, "あ\u{1B}") else e {
        Assert(match e {
            encoding.BadInput(offset) { offset == 3 }
            encoding.Unsupported { false }
            OutOfMemory { false }
        })
        return
    }
    Assert(false)
}

test "gb18030 decodes one, two and four bytes, and a four-byte unit broken at its third byte is U+FFFD at its first" (sys: &System) {
    a := sys.arena
    Assert(Gb18030.Decode(a, b"a\x80\xA2\xE3\x90\x30\x81\x30\x81\x35\xF4\x37").Must() == "a\u{20AC}\u{20AC}\u{10000}\u{E7C7}")
    Assert(Gb18030.Decode(a, b"\x81\x30\xFF").Must() == "\u{FFFD}0\u{FFFD}")
    Assert(Gb18030.Decode(a, b"\x81\x30\x81").Must() == "\u{FFFD}")
    Assert(Gb18030.Decode(a, b"\x84\x31\xA5\x30").Must() == "\u{FFFD}")
    Assert(Gbk.Decode(a, b"\x90\x30\x81\x30").Must() == "\u{10000}")
}

test "gb18030 encodes every scalar but U+E5E5, the side table's PUA as two bytes; GBK writes U+20AC as 0x80 and nothing as four bytes" (sys: &System) {
    a := sys.arena
    Assert(simd.Equal(Gb18030.Encode(a, "\u{20AC}\u{10000}\u{E78D}\u{10FFFF}").Must(), b"\xA2\xE3\x90\x30\x81\x30\xA6\xD9\xE3\x32\x9A\x35"))
    Assert(simd.Equal(Gbk.Encode(a, "\u{20AC}\u{4E02}").Must(), b"\x80\x81\x40"))
    Assert(EncodeFails(a, Gb18030, "\u{E5E5}"))
    Assert(EncodeFails(a, Gbk, "\u{10000}"))
}

test "a gb18030 Decoder holds a four-byte unit cut at every point" (sys: &System) {
    a := sys.arena
    whole: [6]u8 = [0x61, 0x90, 0x30, 0x81, 0x30, 0x62]
    for cut in 0..7 {
        var d := Gb18030.Decoder()
        var t := Text.New(a)
        t.Push(d.Push(a, whole[0..cut], false).Must()).Must()
        t.Push(d.Push(a, whole[cut..], true).Must()).Must()
        Assert(t.Str() == "a\u{10000}b")
    }
}

test "Big5 decodes four pointers to two scalars each, and encodes the box-drawing duplicates by their last pointer" (sys: &System) {
    a := sys.arena
    Assert(Big5.Decode(a, b"\x88\x62\x88\x64\xA4\x40").Must() == "\u{CA}\u{304}\u{CA}\u{30C}一")
    Assert(Big5.DecodeStrict(a, b"\x88\xA5").Must() == "\u{EA}\u{30C}")
    Assert(simd.Equal(Big5.Encode(a, "一\u{2550}").Must(), b"\xA4\x40\xF9\xF9"))
    Assert(Big5.Decode(a, b"\x80\xFFz").Must() == "\u{FFFD}\u{FFFD}z")
}

test "EUC-KR decodes and encodes Hangul through its index, and a lead before ASCII is U+FFFD and the ASCII" (sys: &System) {
    a := sys.arena
    Assert(EucKr.Decode(a, b"\xB0\xA1\xC7\xD1").Must() == "가한")
    Assert(simd.Equal(EucKr.Encode(a, "가한a").Must(), b"\xB0\xA1\xC7\xD1a"))
    Assert(EucKr.Decode(a, b"\xB00").Must() == "\u{FFFD}0")
    Assert(EncodeFails(a, EucKr, "あ\u{1F600}"))
}

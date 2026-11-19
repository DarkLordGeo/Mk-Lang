// Why: str.ToLower and str.ToUpper need Unicode's case mapping without a
// runtime table load or a dependency outside the prelude.
// What: ToLower and ToUpper, the simple one-to-one case mapping of Unicode
// (UnicodeData fields 12 and 13, as Go's unicode.ToLower and ToUpper apply
// it), into a new Text in an arena (layer 1); CaseMapped, CaseOf, PutScalar
// and the generated CaseTable.
// How: ASCII maps by arithmetic; other scalars by binary search over
// CaseTable, generated from Go's unicode.CaseRanges. A mapping can change a
// scalar's encoded length (U+023A becomes U+2C65, 2 bytes to 3), so the text
// is reserved at the input's length and grows only then.

// CaseRange maps lo..=hi by adding up (upper) or low (lower), or, when the
// delta is CaseAlternate, by alternating case from lo (upper at even
// offsets).
struct CaseRange {
    lo: u32
    hi: u32
    up: i32
    low: i32
}

// CaseAlternate is Go's unicode.UpperLower: MaxRune + 1.
const CaseAlternate: i32 = 0x110000

/// A copy of the text in a with each scalar lowercased by Unicode's simple
/// one-to-one mapping, the same for every locale: "ÀÉ İ" gives "àé i". A
/// scalar maps to one scalar, whose encoding may be longer. For caseless
/// matching use std.text's FoldCase. OutOfMemory when the arena cannot grow.
/// O(n log k) for k case ranges, ASCII O(1) per byte; allocates about n
/// bytes, once in the common case.
export str.ToLower(self, a: &Arena): OutOfMemory!str from a {
    return try CaseMapped(self, a, false)
}

/// A copy of the text in a with each scalar uppercased by Unicode's simple
/// one-to-one mapping, as ToLower: "Straße" gives "STRAßE", since Unicode's
/// simple mapping gives ß no uppercase. OutOfMemory when the arena cannot grow.
/// O(n log k) for k case ranges, ASCII O(1) per byte; allocates about n bytes,
/// as ToLower.
export str.ToUpper(self, a: &Arena): OutOfMemory!str from a {
    return try CaseMapped(self, a, true)
}

// CaseMapped copies s into a new Text in a with every scalar mapped. The
// output is staged 64 bytes at a time in the frame, then appended; with
// the reserve, the common case makes one arena allocation of s.Len() bytes.
CaseMapped(s: str, a: &Arena, upper: bool): OutOfMemory!str from a {
    var t := Text.New(a)
    try t.Reserve(s.Len())
    b := s.AsBytes()
    n := b.Len()
    var buf: [68]u8 = [0; 68]
    var o := 0
    var i := 0
    for i < n {
        c := b[i]
        if c < 0x80 {
            var m := c
            if upper {
                if c >= 0x61 && c <= 0x7A {
                    m = c - 0x20
                }
            } else if c >= 0x41 && c <= 0x5A {
                m = c + 0x20
            }
            buf[o] = m
            o += 1
            i += 1
        } else {
            k := ScalarLen(c)
            var v := u32(c) & (0x7F >> Trunc[u32](k))
            for j in 1..k {
                v = (v << 6) | (u32(b[i + j]) & 0x3F)
            }
            o += PutScalar(mut buf[o..o + 4], CaseOf(v, upper))
            i += k
        }
        if o >= 64 {
            try t.PushBytes(buf[0..o])
            o = 0
        }
    }
    try t.PushBytes(buf[0..o])
    return TextInto(t)
}

// CaseOf maps the scalar v: Go's unicode.to over CaseTable. A scalar no
// range holds maps to itself. The table is read through one reference: at
// compile time a module const named by value is copied whole at each use;
// natively both forms are the same load.
CaseOf(v: u32, upper: bool): u32 {
    tab := &CaseTable
    var lo := 0
    var hi := tab.Len()
    for lo < hi {
        m := (lo + hi) / 2
        r := tab[m]
        if v < r.lo {
            hi = m
        } else if v > r.hi {
            lo = m + 1
        } else {
            delta := if upper { r.up } else { r.low }
            if delta == CaseAlternate {
                // Upper at even offsets from lo, lower at odd ones.
                bit: u32 = if upper { 0 } else { 1 }
                return r.lo + (((v - r.lo) & ~Trunc[u32](1)) | bit)
            }
            return Trunc[u32](i64(v) + i64(delta))
        }
    }
    return v
}

// PutScalar encodes the scalar v (never a surrogate, at most 10FFFF) into
// b, which has room for 4 bytes, and returns its length.
PutScalar(b: mut []u8, v: u32): int {
    if v < 0x80 {
        b[0] = Trunc[u8](v)
        return 1
    }
    if v < 0x800 {
        b[0] = Trunc[u8](0xC0 | (v >> 6))
        b[1] = Trunc[u8](0x80 | (v & 0x3F))
        return 2
    }
    if v < 0x10000 {
        b[0] = Trunc[u8](0xE0 | (v >> 12))
        b[1] = Trunc[u8](0x80 | ((v >> 6) & 0x3F))
        b[2] = Trunc[u8](0x80 | (v & 0x3F))
        return 3
    }
    b[0] = Trunc[u8](0xF0 | (v >> 18))
    b[1] = Trunc[u8](0x80 | ((v >> 12) & 0x3F))
    b[2] = Trunc[u8](0x80 | ((v >> 6) & 0x3F))
    b[3] = Trunc[u8](0x80 | (v & 0x3F))
    return 4
}

// BEGIN GENERATED CaseTable by TestTextTablesUpToDate (std/std-text_tables_test.go); do not edit
// unicode.Version: 17.0.0. unicode.CaseRanges, ascending: scalars lo..=hi map
// to scalar + up (upper) and scalar + low (lower); a delta of
// CaseAlternate means upper at even offsets from lo, lower at odd ones.
const CaseTable: [336]CaseRange = [
    CaseRange(lo: 0x0041, hi: 0x005A, up: 0, low: 32),
    CaseRange(lo: 0x0061, hi: 0x007A, up: -32, low: 0),
    CaseRange(lo: 0x00B5, hi: 0x00B5, up: 743, low: 0),
    CaseRange(lo: 0x00C0, hi: 0x00D6, up: 0, low: 32),
    CaseRange(lo: 0x00D8, hi: 0x00DE, up: 0, low: 32),
    CaseRange(lo: 0x00E0, hi: 0x00F6, up: -32, low: 0),
    CaseRange(lo: 0x00F8, hi: 0x00FE, up: -32, low: 0),
    CaseRange(lo: 0x00FF, hi: 0x00FF, up: 121, low: 0),
    CaseRange(lo: 0x0100, hi: 0x012F, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0130, hi: 0x0130, up: 0, low: -199),
    CaseRange(lo: 0x0131, hi: 0x0131, up: -232, low: 0),
    CaseRange(lo: 0x0132, hi: 0x0137, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0139, hi: 0x0148, up: 1114112, low: 1114112),
    CaseRange(lo: 0x014A, hi: 0x0177, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0178, hi: 0x0178, up: 0, low: -121),
    CaseRange(lo: 0x0179, hi: 0x017E, up: 1114112, low: 1114112),
    CaseRange(lo: 0x017F, hi: 0x017F, up: -300, low: 0),
    CaseRange(lo: 0x0180, hi: 0x0180, up: 195, low: 0),
    CaseRange(lo: 0x0181, hi: 0x0181, up: 0, low: 210),
    CaseRange(lo: 0x0182, hi: 0x0185, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0186, hi: 0x0186, up: 0, low: 206),
    CaseRange(lo: 0x0187, hi: 0x0188, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0189, hi: 0x018A, up: 0, low: 205),
    CaseRange(lo: 0x018B, hi: 0x018C, up: 1114112, low: 1114112),
    CaseRange(lo: 0x018E, hi: 0x018E, up: 0, low: 79),
    CaseRange(lo: 0x018F, hi: 0x018F, up: 0, low: 202),
    CaseRange(lo: 0x0190, hi: 0x0190, up: 0, low: 203),
    CaseRange(lo: 0x0191, hi: 0x0192, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0193, hi: 0x0193, up: 0, low: 205),
    CaseRange(lo: 0x0194, hi: 0x0194, up: 0, low: 207),
    CaseRange(lo: 0x0195, hi: 0x0195, up: 97, low: 0),
    CaseRange(lo: 0x0196, hi: 0x0196, up: 0, low: 211),
    CaseRange(lo: 0x0197, hi: 0x0197, up: 0, low: 209),
    CaseRange(lo: 0x0198, hi: 0x0199, up: 1114112, low: 1114112),
    CaseRange(lo: 0x019A, hi: 0x019A, up: 163, low: 0),
    CaseRange(lo: 0x019B, hi: 0x019B, up: 42561, low: 0),
    CaseRange(lo: 0x019C, hi: 0x019C, up: 0, low: 211),
    CaseRange(lo: 0x019D, hi: 0x019D, up: 0, low: 213),
    CaseRange(lo: 0x019E, hi: 0x019E, up: 130, low: 0),
    CaseRange(lo: 0x019F, hi: 0x019F, up: 0, low: 214),
    CaseRange(lo: 0x01A0, hi: 0x01A5, up: 1114112, low: 1114112),
    CaseRange(lo: 0x01A6, hi: 0x01A6, up: 0, low: 218),
    CaseRange(lo: 0x01A7, hi: 0x01A8, up: 1114112, low: 1114112),
    CaseRange(lo: 0x01A9, hi: 0x01A9, up: 0, low: 218),
    CaseRange(lo: 0x01AC, hi: 0x01AD, up: 1114112, low: 1114112),
    CaseRange(lo: 0x01AE, hi: 0x01AE, up: 0, low: 218),
    CaseRange(lo: 0x01AF, hi: 0x01B0, up: 1114112, low: 1114112),
    CaseRange(lo: 0x01B1, hi: 0x01B2, up: 0, low: 217),
    CaseRange(lo: 0x01B3, hi: 0x01B6, up: 1114112, low: 1114112),
    CaseRange(lo: 0x01B7, hi: 0x01B7, up: 0, low: 219),
    CaseRange(lo: 0x01B8, hi: 0x01B9, up: 1114112, low: 1114112),
    CaseRange(lo: 0x01BC, hi: 0x01BD, up: 1114112, low: 1114112),
    CaseRange(lo: 0x01BF, hi: 0x01BF, up: 56, low: 0),
    CaseRange(lo: 0x01C4, hi: 0x01C4, up: 0, low: 2),
    CaseRange(lo: 0x01C5, hi: 0x01C5, up: -1, low: 1),
    CaseRange(lo: 0x01C6, hi: 0x01C6, up: -2, low: 0),
    CaseRange(lo: 0x01C7, hi: 0x01C7, up: 0, low: 2),
    CaseRange(lo: 0x01C8, hi: 0x01C8, up: -1, low: 1),
    CaseRange(lo: 0x01C9, hi: 0x01C9, up: -2, low: 0),
    CaseRange(lo: 0x01CA, hi: 0x01CA, up: 0, low: 2),
    CaseRange(lo: 0x01CB, hi: 0x01CB, up: -1, low: 1),
    CaseRange(lo: 0x01CC, hi: 0x01CC, up: -2, low: 0),
    CaseRange(lo: 0x01CD, hi: 0x01DC, up: 1114112, low: 1114112),
    CaseRange(lo: 0x01DD, hi: 0x01DD, up: -79, low: 0),
    CaseRange(lo: 0x01DE, hi: 0x01EF, up: 1114112, low: 1114112),
    CaseRange(lo: 0x01F1, hi: 0x01F1, up: 0, low: 2),
    CaseRange(lo: 0x01F2, hi: 0x01F2, up: -1, low: 1),
    CaseRange(lo: 0x01F3, hi: 0x01F3, up: -2, low: 0),
    CaseRange(lo: 0x01F4, hi: 0x01F5, up: 1114112, low: 1114112),
    CaseRange(lo: 0x01F6, hi: 0x01F6, up: 0, low: -97),
    CaseRange(lo: 0x01F7, hi: 0x01F7, up: 0, low: -56),
    CaseRange(lo: 0x01F8, hi: 0x021F, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0220, hi: 0x0220, up: 0, low: -130),
    CaseRange(lo: 0x0222, hi: 0x0233, up: 1114112, low: 1114112),
    CaseRange(lo: 0x023A, hi: 0x023A, up: 0, low: 10795),
    CaseRange(lo: 0x023B, hi: 0x023C, up: 1114112, low: 1114112),
    CaseRange(lo: 0x023D, hi: 0x023D, up: 0, low: -163),
    CaseRange(lo: 0x023E, hi: 0x023E, up: 0, low: 10792),
    CaseRange(lo: 0x023F, hi: 0x0240, up: 10815, low: 0),
    CaseRange(lo: 0x0241, hi: 0x0242, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0243, hi: 0x0243, up: 0, low: -195),
    CaseRange(lo: 0x0244, hi: 0x0244, up: 0, low: 69),
    CaseRange(lo: 0x0245, hi: 0x0245, up: 0, low: 71),
    CaseRange(lo: 0x0246, hi: 0x024F, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0250, hi: 0x0250, up: 10783, low: 0),
    CaseRange(lo: 0x0251, hi: 0x0251, up: 10780, low: 0),
    CaseRange(lo: 0x0252, hi: 0x0252, up: 10782, low: 0),
    CaseRange(lo: 0x0253, hi: 0x0253, up: -210, low: 0),
    CaseRange(lo: 0x0254, hi: 0x0254, up: -206, low: 0),
    CaseRange(lo: 0x0256, hi: 0x0257, up: -205, low: 0),
    CaseRange(lo: 0x0259, hi: 0x0259, up: -202, low: 0),
    CaseRange(lo: 0x025B, hi: 0x025B, up: -203, low: 0),
    CaseRange(lo: 0x025C, hi: 0x025C, up: 42319, low: 0),
    CaseRange(lo: 0x0260, hi: 0x0260, up: -205, low: 0),
    CaseRange(lo: 0x0261, hi: 0x0261, up: 42315, low: 0),
    CaseRange(lo: 0x0263, hi: 0x0263, up: -207, low: 0),
    CaseRange(lo: 0x0264, hi: 0x0264, up: 42343, low: 0),
    CaseRange(lo: 0x0265, hi: 0x0265, up: 42280, low: 0),
    CaseRange(lo: 0x0266, hi: 0x0266, up: 42308, low: 0),
    CaseRange(lo: 0x0268, hi: 0x0268, up: -209, low: 0),
    CaseRange(lo: 0x0269, hi: 0x0269, up: -211, low: 0),
    CaseRange(lo: 0x026A, hi: 0x026A, up: 42308, low: 0),
    CaseRange(lo: 0x026B, hi: 0x026B, up: 10743, low: 0),
    CaseRange(lo: 0x026C, hi: 0x026C, up: 42305, low: 0),
    CaseRange(lo: 0x026F, hi: 0x026F, up: -211, low: 0),
    CaseRange(lo: 0x0271, hi: 0x0271, up: 10749, low: 0),
    CaseRange(lo: 0x0272, hi: 0x0272, up: -213, low: 0),
    CaseRange(lo: 0x0275, hi: 0x0275, up: -214, low: 0),
    CaseRange(lo: 0x027D, hi: 0x027D, up: 10727, low: 0),
    CaseRange(lo: 0x0280, hi: 0x0280, up: -218, low: 0),
    CaseRange(lo: 0x0282, hi: 0x0282, up: 42307, low: 0),
    CaseRange(lo: 0x0283, hi: 0x0283, up: -218, low: 0),
    CaseRange(lo: 0x0287, hi: 0x0287, up: 42282, low: 0),
    CaseRange(lo: 0x0288, hi: 0x0288, up: -218, low: 0),
    CaseRange(lo: 0x0289, hi: 0x0289, up: -69, low: 0),
    CaseRange(lo: 0x028A, hi: 0x028B, up: -217, low: 0),
    CaseRange(lo: 0x028C, hi: 0x028C, up: -71, low: 0),
    CaseRange(lo: 0x0292, hi: 0x0292, up: -219, low: 0),
    CaseRange(lo: 0x029D, hi: 0x029D, up: 42261, low: 0),
    CaseRange(lo: 0x029E, hi: 0x029E, up: 42258, low: 0),
    CaseRange(lo: 0x0345, hi: 0x0345, up: 84, low: 0),
    CaseRange(lo: 0x0370, hi: 0x0373, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0376, hi: 0x0377, up: 1114112, low: 1114112),
    CaseRange(lo: 0x037B, hi: 0x037D, up: 130, low: 0),
    CaseRange(lo: 0x037F, hi: 0x037F, up: 0, low: 116),
    CaseRange(lo: 0x0386, hi: 0x0386, up: 0, low: 38),
    CaseRange(lo: 0x0388, hi: 0x038A, up: 0, low: 37),
    CaseRange(lo: 0x038C, hi: 0x038C, up: 0, low: 64),
    CaseRange(lo: 0x038E, hi: 0x038F, up: 0, low: 63),
    CaseRange(lo: 0x0391, hi: 0x03A1, up: 0, low: 32),
    CaseRange(lo: 0x03A3, hi: 0x03AB, up: 0, low: 32),
    CaseRange(lo: 0x03AC, hi: 0x03AC, up: -38, low: 0),
    CaseRange(lo: 0x03AD, hi: 0x03AF, up: -37, low: 0),
    CaseRange(lo: 0x03B1, hi: 0x03C1, up: -32, low: 0),
    CaseRange(lo: 0x03C2, hi: 0x03C2, up: -31, low: 0),
    CaseRange(lo: 0x03C3, hi: 0x03CB, up: -32, low: 0),
    CaseRange(lo: 0x03CC, hi: 0x03CC, up: -64, low: 0),
    CaseRange(lo: 0x03CD, hi: 0x03CE, up: -63, low: 0),
    CaseRange(lo: 0x03CF, hi: 0x03CF, up: 0, low: 8),
    CaseRange(lo: 0x03D0, hi: 0x03D0, up: -62, low: 0),
    CaseRange(lo: 0x03D1, hi: 0x03D1, up: -57, low: 0),
    CaseRange(lo: 0x03D5, hi: 0x03D5, up: -47, low: 0),
    CaseRange(lo: 0x03D6, hi: 0x03D6, up: -54, low: 0),
    CaseRange(lo: 0x03D7, hi: 0x03D7, up: -8, low: 0),
    CaseRange(lo: 0x03D8, hi: 0x03EF, up: 1114112, low: 1114112),
    CaseRange(lo: 0x03F0, hi: 0x03F0, up: -86, low: 0),
    CaseRange(lo: 0x03F1, hi: 0x03F1, up: -80, low: 0),
    CaseRange(lo: 0x03F2, hi: 0x03F2, up: 7, low: 0),
    CaseRange(lo: 0x03F3, hi: 0x03F3, up: -116, low: 0),
    CaseRange(lo: 0x03F4, hi: 0x03F4, up: 0, low: -60),
    CaseRange(lo: 0x03F5, hi: 0x03F5, up: -96, low: 0),
    CaseRange(lo: 0x03F7, hi: 0x03F8, up: 1114112, low: 1114112),
    CaseRange(lo: 0x03F9, hi: 0x03F9, up: 0, low: -7),
    CaseRange(lo: 0x03FA, hi: 0x03FB, up: 1114112, low: 1114112),
    CaseRange(lo: 0x03FD, hi: 0x03FF, up: 0, low: -130),
    CaseRange(lo: 0x0400, hi: 0x040F, up: 0, low: 80),
    CaseRange(lo: 0x0410, hi: 0x042F, up: 0, low: 32),
    CaseRange(lo: 0x0430, hi: 0x044F, up: -32, low: 0),
    CaseRange(lo: 0x0450, hi: 0x045F, up: -80, low: 0),
    CaseRange(lo: 0x0460, hi: 0x0481, up: 1114112, low: 1114112),
    CaseRange(lo: 0x048A, hi: 0x04BF, up: 1114112, low: 1114112),
    CaseRange(lo: 0x04C0, hi: 0x04C0, up: 0, low: 15),
    CaseRange(lo: 0x04C1, hi: 0x04CE, up: 1114112, low: 1114112),
    CaseRange(lo: 0x04CF, hi: 0x04CF, up: -15, low: 0),
    CaseRange(lo: 0x04D0, hi: 0x052F, up: 1114112, low: 1114112),
    CaseRange(lo: 0x0531, hi: 0x0556, up: 0, low: 48),
    CaseRange(lo: 0x0561, hi: 0x0586, up: -48, low: 0),
    CaseRange(lo: 0x10A0, hi: 0x10C5, up: 0, low: 7264),
    CaseRange(lo: 0x10C7, hi: 0x10C7, up: 0, low: 7264),
    CaseRange(lo: 0x10CD, hi: 0x10CD, up: 0, low: 7264),
    CaseRange(lo: 0x10D0, hi: 0x10FA, up: 3008, low: 0),
    CaseRange(lo: 0x10FD, hi: 0x10FF, up: 3008, low: 0),
    CaseRange(lo: 0x13A0, hi: 0x13EF, up: 0, low: 38864),
    CaseRange(lo: 0x13F0, hi: 0x13F5, up: 0, low: 8),
    CaseRange(lo: 0x13F8, hi: 0x13FD, up: -8, low: 0),
    CaseRange(lo: 0x1C80, hi: 0x1C80, up: -6254, low: 0),
    CaseRange(lo: 0x1C81, hi: 0x1C81, up: -6253, low: 0),
    CaseRange(lo: 0x1C82, hi: 0x1C82, up: -6244, low: 0),
    CaseRange(lo: 0x1C83, hi: 0x1C84, up: -6242, low: 0),
    CaseRange(lo: 0x1C85, hi: 0x1C85, up: -6243, low: 0),
    CaseRange(lo: 0x1C86, hi: 0x1C86, up: -6236, low: 0),
    CaseRange(lo: 0x1C87, hi: 0x1C87, up: -6181, low: 0),
    CaseRange(lo: 0x1C88, hi: 0x1C88, up: 35266, low: 0),
    CaseRange(lo: 0x1C89, hi: 0x1C8A, up: 1114112, low: 1114112),
    CaseRange(lo: 0x1C90, hi: 0x1CBA, up: 0, low: -3008),
    CaseRange(lo: 0x1CBD, hi: 0x1CBF, up: 0, low: -3008),
    CaseRange(lo: 0x1D79, hi: 0x1D79, up: 35332, low: 0),
    CaseRange(lo: 0x1D7D, hi: 0x1D7D, up: 3814, low: 0),
    CaseRange(lo: 0x1D8E, hi: 0x1D8E, up: 35384, low: 0),
    CaseRange(lo: 0x1E00, hi: 0x1E95, up: 1114112, low: 1114112),
    CaseRange(lo: 0x1E9B, hi: 0x1E9B, up: -59, low: 0),
    CaseRange(lo: 0x1E9E, hi: 0x1E9E, up: 0, low: -7615),
    CaseRange(lo: 0x1EA0, hi: 0x1EFF, up: 1114112, low: 1114112),
    CaseRange(lo: 0x1F00, hi: 0x1F07, up: 8, low: 0),
    CaseRange(lo: 0x1F08, hi: 0x1F0F, up: 0, low: -8),
    CaseRange(lo: 0x1F10, hi: 0x1F15, up: 8, low: 0),
    CaseRange(lo: 0x1F18, hi: 0x1F1D, up: 0, low: -8),
    CaseRange(lo: 0x1F20, hi: 0x1F27, up: 8, low: 0),
    CaseRange(lo: 0x1F28, hi: 0x1F2F, up: 0, low: -8),
    CaseRange(lo: 0x1F30, hi: 0x1F37, up: 8, low: 0),
    CaseRange(lo: 0x1F38, hi: 0x1F3F, up: 0, low: -8),
    CaseRange(lo: 0x1F40, hi: 0x1F45, up: 8, low: 0),
    CaseRange(lo: 0x1F48, hi: 0x1F4D, up: 0, low: -8),
    CaseRange(lo: 0x1F51, hi: 0x1F51, up: 8, low: 0),
    CaseRange(lo: 0x1F53, hi: 0x1F53, up: 8, low: 0),
    CaseRange(lo: 0x1F55, hi: 0x1F55, up: 8, low: 0),
    CaseRange(lo: 0x1F57, hi: 0x1F57, up: 8, low: 0),
    CaseRange(lo: 0x1F59, hi: 0x1F59, up: 0, low: -8),
    CaseRange(lo: 0x1F5B, hi: 0x1F5B, up: 0, low: -8),
    CaseRange(lo: 0x1F5D, hi: 0x1F5D, up: 0, low: -8),
    CaseRange(lo: 0x1F5F, hi: 0x1F5F, up: 0, low: -8),
    CaseRange(lo: 0x1F60, hi: 0x1F67, up: 8, low: 0),
    CaseRange(lo: 0x1F68, hi: 0x1F6F, up: 0, low: -8),
    CaseRange(lo: 0x1F70, hi: 0x1F71, up: 74, low: 0),
    CaseRange(lo: 0x1F72, hi: 0x1F75, up: 86, low: 0),
    CaseRange(lo: 0x1F76, hi: 0x1F77, up: 100, low: 0),
    CaseRange(lo: 0x1F78, hi: 0x1F79, up: 128, low: 0),
    CaseRange(lo: 0x1F7A, hi: 0x1F7B, up: 112, low: 0),
    CaseRange(lo: 0x1F7C, hi: 0x1F7D, up: 126, low: 0),
    CaseRange(lo: 0x1F80, hi: 0x1F87, up: 8, low: 0),
    CaseRange(lo: 0x1F88, hi: 0x1F8F, up: 0, low: -8),
    CaseRange(lo: 0x1F90, hi: 0x1F97, up: 8, low: 0),
    CaseRange(lo: 0x1F98, hi: 0x1F9F, up: 0, low: -8),
    CaseRange(lo: 0x1FA0, hi: 0x1FA7, up: 8, low: 0),
    CaseRange(lo: 0x1FA8, hi: 0x1FAF, up: 0, low: -8),
    CaseRange(lo: 0x1FB0, hi: 0x1FB1, up: 8, low: 0),
    CaseRange(lo: 0x1FB3, hi: 0x1FB3, up: 9, low: 0),
    CaseRange(lo: 0x1FB8, hi: 0x1FB9, up: 0, low: -8),
    CaseRange(lo: 0x1FBA, hi: 0x1FBB, up: 0, low: -74),
    CaseRange(lo: 0x1FBC, hi: 0x1FBC, up: 0, low: -9),
    CaseRange(lo: 0x1FBE, hi: 0x1FBE, up: -7205, low: 0),
    CaseRange(lo: 0x1FC3, hi: 0x1FC3, up: 9, low: 0),
    CaseRange(lo: 0x1FC8, hi: 0x1FCB, up: 0, low: -86),
    CaseRange(lo: 0x1FCC, hi: 0x1FCC, up: 0, low: -9),
    CaseRange(lo: 0x1FD0, hi: 0x1FD1, up: 8, low: 0),
    CaseRange(lo: 0x1FD8, hi: 0x1FD9, up: 0, low: -8),
    CaseRange(lo: 0x1FDA, hi: 0x1FDB, up: 0, low: -100),
    CaseRange(lo: 0x1FE0, hi: 0x1FE1, up: 8, low: 0),
    CaseRange(lo: 0x1FE5, hi: 0x1FE5, up: 7, low: 0),
    CaseRange(lo: 0x1FE8, hi: 0x1FE9, up: 0, low: -8),
    CaseRange(lo: 0x1FEA, hi: 0x1FEB, up: 0, low: -112),
    CaseRange(lo: 0x1FEC, hi: 0x1FEC, up: 0, low: -7),
    CaseRange(lo: 0x1FF3, hi: 0x1FF3, up: 9, low: 0),
    CaseRange(lo: 0x1FF8, hi: 0x1FF9, up: 0, low: -128),
    CaseRange(lo: 0x1FFA, hi: 0x1FFB, up: 0, low: -126),
    CaseRange(lo: 0x1FFC, hi: 0x1FFC, up: 0, low: -9),
    CaseRange(lo: 0x2126, hi: 0x2126, up: 0, low: -7517),
    CaseRange(lo: 0x212A, hi: 0x212A, up: 0, low: -8383),
    CaseRange(lo: 0x212B, hi: 0x212B, up: 0, low: -8262),
    CaseRange(lo: 0x2132, hi: 0x2132, up: 0, low: 28),
    CaseRange(lo: 0x214E, hi: 0x214E, up: -28, low: 0),
    CaseRange(lo: 0x2160, hi: 0x216F, up: 0, low: 16),
    CaseRange(lo: 0x2170, hi: 0x217F, up: -16, low: 0),
    CaseRange(lo: 0x2183, hi: 0x2184, up: 1114112, low: 1114112),
    CaseRange(lo: 0x24B6, hi: 0x24CF, up: 0, low: 26),
    CaseRange(lo: 0x24D0, hi: 0x24E9, up: -26, low: 0),
    CaseRange(lo: 0x2C00, hi: 0x2C2F, up: 0, low: 48),
    CaseRange(lo: 0x2C30, hi: 0x2C5F, up: -48, low: 0),
    CaseRange(lo: 0x2C60, hi: 0x2C61, up: 1114112, low: 1114112),
    CaseRange(lo: 0x2C62, hi: 0x2C62, up: 0, low: -10743),
    CaseRange(lo: 0x2C63, hi: 0x2C63, up: 0, low: -3814),
    CaseRange(lo: 0x2C64, hi: 0x2C64, up: 0, low: -10727),
    CaseRange(lo: 0x2C65, hi: 0x2C65, up: -10795, low: 0),
    CaseRange(lo: 0x2C66, hi: 0x2C66, up: -10792, low: 0),
    CaseRange(lo: 0x2C67, hi: 0x2C6C, up: 1114112, low: 1114112),
    CaseRange(lo: 0x2C6D, hi: 0x2C6D, up: 0, low: -10780),
    CaseRange(lo: 0x2C6E, hi: 0x2C6E, up: 0, low: -10749),
    CaseRange(lo: 0x2C6F, hi: 0x2C6F, up: 0, low: -10783),
    CaseRange(lo: 0x2C70, hi: 0x2C70, up: 0, low: -10782),
    CaseRange(lo: 0x2C72, hi: 0x2C73, up: 1114112, low: 1114112),
    CaseRange(lo: 0x2C75, hi: 0x2C76, up: 1114112, low: 1114112),
    CaseRange(lo: 0x2C7E, hi: 0x2C7F, up: 0, low: -10815),
    CaseRange(lo: 0x2C80, hi: 0x2CE3, up: 1114112, low: 1114112),
    CaseRange(lo: 0x2CEB, hi: 0x2CEE, up: 1114112, low: 1114112),
    CaseRange(lo: 0x2CF2, hi: 0x2CF3, up: 1114112, low: 1114112),
    CaseRange(lo: 0x2D00, hi: 0x2D25, up: -7264, low: 0),
    CaseRange(lo: 0x2D27, hi: 0x2D27, up: -7264, low: 0),
    CaseRange(lo: 0x2D2D, hi: 0x2D2D, up: -7264, low: 0),
    CaseRange(lo: 0xA640, hi: 0xA66D, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA680, hi: 0xA69B, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA722, hi: 0xA72F, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA732, hi: 0xA76F, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA779, hi: 0xA77C, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA77D, hi: 0xA77D, up: 0, low: -35332),
    CaseRange(lo: 0xA77E, hi: 0xA787, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA78B, hi: 0xA78C, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA78D, hi: 0xA78D, up: 0, low: -42280),
    CaseRange(lo: 0xA790, hi: 0xA793, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA794, hi: 0xA794, up: 48, low: 0),
    CaseRange(lo: 0xA796, hi: 0xA7A9, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA7AA, hi: 0xA7AA, up: 0, low: -42308),
    CaseRange(lo: 0xA7AB, hi: 0xA7AB, up: 0, low: -42319),
    CaseRange(lo: 0xA7AC, hi: 0xA7AC, up: 0, low: -42315),
    CaseRange(lo: 0xA7AD, hi: 0xA7AD, up: 0, low: -42305),
    CaseRange(lo: 0xA7AE, hi: 0xA7AE, up: 0, low: -42308),
    CaseRange(lo: 0xA7B0, hi: 0xA7B0, up: 0, low: -42258),
    CaseRange(lo: 0xA7B1, hi: 0xA7B1, up: 0, low: -42282),
    CaseRange(lo: 0xA7B2, hi: 0xA7B2, up: 0, low: -42261),
    CaseRange(lo: 0xA7B3, hi: 0xA7B3, up: 0, low: 928),
    CaseRange(lo: 0xA7B4, hi: 0xA7C3, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA7C4, hi: 0xA7C4, up: 0, low: -48),
    CaseRange(lo: 0xA7C5, hi: 0xA7C5, up: 0, low: -42307),
    CaseRange(lo: 0xA7C6, hi: 0xA7C6, up: 0, low: -35384),
    CaseRange(lo: 0xA7C7, hi: 0xA7CA, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA7CB, hi: 0xA7CB, up: 0, low: -42343),
    CaseRange(lo: 0xA7CC, hi: 0xA7DB, up: 1114112, low: 1114112),
    CaseRange(lo: 0xA7DC, hi: 0xA7DC, up: 0, low: -42561),
    CaseRange(lo: 0xA7F5, hi: 0xA7F6, up: 1114112, low: 1114112),
    CaseRange(lo: 0xAB53, hi: 0xAB53, up: -928, low: 0),
    CaseRange(lo: 0xAB70, hi: 0xABBF, up: -38864, low: 0),
    CaseRange(lo: 0xFF21, hi: 0xFF3A, up: 0, low: 32),
    CaseRange(lo: 0xFF41, hi: 0xFF5A, up: -32, low: 0),
    CaseRange(lo: 0x10400, hi: 0x10427, up: 0, low: 40),
    CaseRange(lo: 0x10428, hi: 0x1044F, up: -40, low: 0),
    CaseRange(lo: 0x104B0, hi: 0x104D3, up: 0, low: 40),
    CaseRange(lo: 0x104D8, hi: 0x104FB, up: -40, low: 0),
    CaseRange(lo: 0x10570, hi: 0x1057A, up: 0, low: 39),
    CaseRange(lo: 0x1057C, hi: 0x1058A, up: 0, low: 39),
    CaseRange(lo: 0x1058C, hi: 0x10592, up: 0, low: 39),
    CaseRange(lo: 0x10594, hi: 0x10595, up: 0, low: 39),
    CaseRange(lo: 0x10597, hi: 0x105A1, up: -39, low: 0),
    CaseRange(lo: 0x105A3, hi: 0x105B1, up: -39, low: 0),
    CaseRange(lo: 0x105B3, hi: 0x105B9, up: -39, low: 0),
    CaseRange(lo: 0x105BB, hi: 0x105BC, up: -39, low: 0),
    CaseRange(lo: 0x10C80, hi: 0x10CB2, up: 0, low: 64),
    CaseRange(lo: 0x10CC0, hi: 0x10CF2, up: -64, low: 0),
    CaseRange(lo: 0x10D50, hi: 0x10D65, up: 0, low: 32),
    CaseRange(lo: 0x10D70, hi: 0x10D85, up: -32, low: 0),
    CaseRange(lo: 0x118A0, hi: 0x118BF, up: 0, low: 32),
    CaseRange(lo: 0x118C0, hi: 0x118DF, up: -32, low: 0),
    CaseRange(lo: 0x16E40, hi: 0x16E5F, up: 0, low: 32),
    CaseRange(lo: 0x16E60, hi: 0x16E7F, up: -32, low: 0),
    CaseRange(lo: 0x16EA0, hi: 0x16EB8, up: 0, low: 27),
    CaseRange(lo: 0x16EBB, hi: 0x16ED3, up: -27, low: 0),
    CaseRange(lo: 0x1E900, hi: 0x1E921, up: 0, low: 34),
    CaseRange(lo: 0x1E922, hi: 0x1E943, up: -34, low: 0),
]
// END GENERATED CaseTable

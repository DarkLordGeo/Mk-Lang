// Why: the WHATWG Encoding Standard's multi-byte encodings carry large
// index tables; kept in a module of their own, as std.time.tzdata is, they
// reach only programs that import std.encoding.cjk, and the import line
// shows a reader that a program carries them.
//
// What: the Encodings Gbk, Gb18030, Big5, EucJp, Iso2022Jp, ShiftJis and
// EucKr, each made by Encoding.Of from a Codec of its decoder and encoder,
// and Lookup, the standard's get-an-encoding over every label. Also the
// handler helpers One, Short, Restore, Dec2, Dec3, Put1 and Put2.
//
// How: each table in tables.mk is reached only through the codec functions
// of the encodings that read it, so a program links the tables of the
// encodings it names (TestStdEncodingCjk_LinksOnlyNamed); Lookup names them
// all. The decoders follow the standard handler by handler: a byte the
// standard restores to the input is not part of the malformed unit before
// it and is read again, and a stream that ends inside a unit ends in one
// U+FFFD, then the bytes it restores, so ISO-2022-JP's `ESC $` at the end
// is U+FFFD and `$`, and `ESC $ B ESC $` two U+FFFD. The two-pass driver,
// strictness and the state between chunks are std.encoding's codec.mk.

import std.encoding
import std.simd

/// Gbk is the WHATWG GBK encoding: gb18030's decoder, and gb18030's encoder
/// limited to GBK, so U+20AC is the byte 0x80 and a code point with no
/// two-byte form has no bytes.
export const Gbk: encoding.Encoding = encoding.Encoding.Of(encoding.Codec(name: GbkName, decode: Gb18030Decode, encode: GbkEncode))

/// Gb18030 is the WHATWG gb18030 encoding: one, two or four bytes, every
/// scalar but U+E5E5 encodable.
export const Gb18030: encoding.Encoding = encoding.Encoding.Of(encoding.Codec(name: Gb18030Name, decode: Gb18030Decode, encode: Gb18030Encode))

/// Big5 is the WHATWG Big5 encoding, with the Hong Kong Supplementary
/// Character Set.
export const Big5: encoding.Encoding = encoding.Encoding.Of(encoding.Codec(name: Big5Name, decode: Big5Decode, encode: Big5Encode))

/// EucJp is the WHATWG EUC-JP encoding; JIS X 0212 decodes and does not
/// encode.
export const EucJp: encoding.Encoding = encoding.Encoding.Of(encoding.Codec(name: EucJpName, decode: EucJpDecode, encode: EucJpEncode))

/// Iso2022Jp is the WHATWG ISO-2022-JP encoding: a stateful encoding whose
/// Decoder keeps its mode between chunks, and whose Encode ends in ASCII.
export const Iso2022Jp: encoding.Encoding = encoding.Encoding.Of(encoding.Codec(name: Iso2022JpName, decode: Iso2022JpDecode, encode: Iso2022JpEncode))

/// ShiftJis is the WHATWG Shift_JIS encoding, with Windows' EUDC range
/// decoding to the Private Use Area.
export const ShiftJis: encoding.Encoding = encoding.Encoding.Of(encoding.Codec(name: ShiftJisName, decode: ShiftJisDecode, encode: ShiftJisEncode))

/// EucKr is the WHATWG EUC-KR encoding (Windows code page 949).
export const EucKr: encoding.Encoding = encoding.Encoding.Of(encoding.Codec(name: EucKrName, decode: EucKrDecode, encode: EucKrEncode))

GbkName(): str {
    return "GBK"
}

Gb18030Name(): str {
    return "gb18030"
}

Big5Name(): str {
    return "Big5"
}

EucJpName(): str {
    return "EUC-JP"
}

Iso2022JpName(): str {
    return "ISO-2022-JP"
}

ShiftJisName(): str {
    return "Shift_JIS"
}

EucKrName(): str {
    return "EUC-KR"
}

/// Lookup is the encoding a label names, by the Encoding Standard's get an
/// encoding, over every label of the standard: encoding.Lookup's, and the
/// labels of the multi-byte encodings this module builds, for which
/// encoding.Lookup fails with Unsupported. Fails with UnknownLabel for a
/// label the standard does not have. O(log n) in the label tables plus the
/// label's length; allocates nothing; names every encoding, so it links
/// every table of std.encoding and of this module.
export Lookup(label: str): encoding.UnknownLabel!encoding.Encoding {
    e := encoding.Lookup(label) else {
        ByLabel(label) else {
            fail encoding.UnknownLabel
        }
    }
    return e
}

// Space is ASCII whitespace as the standard's get an encoding strips it.
Space(c: u8): bool {
    return c == 0x09 || c == 0x0A || c == 0x0C || c == 0x0D || c == 0x20
}

// ByLabel is the encoding of this module that label names, none for a
// label of another encoding or of none.
ByLabel(label: str): ?encoding.Encoding {
    b := label.AsBytes()
    var lo := 0
    var hi := b.Len()
    for lo < hi && Space(b[lo]) {
        lo += 1
    }
    for hi > lo && Space(b[hi - 1]) {
        hi -= 1
    }
    if hi - lo > LongestLabel {
        return none
    }
    var key: [LongestLabel]u8 = [0; LongestLabel]
    for i in lo..hi {
        c := b[i]
        key[i - lo] = if c >= b'A' && c <= b'Z' { c + 32 } else { c }
    }
    k := key[0..hi - lo]
    labels := Labels
    var l := 0
    var r := labels.Len()
    for l < r {
        mid := l + (r - l) / 2
        c := simd.Compare(labels[mid].AsBytes(), k)
        if c < 0 {
            l = mid + 1
        } else if c > 0 {
            r = mid
        } else {
            return some(ById[int(LabelIds[mid])])
        }
    }
    return none
}

// Unit is what a decoder gives for one unit: Codec.decode's result.
type Unit = (n: int, c: u32, c2: u32, state: u8)

// Encoded is what an encoder gives for one scalar: Codec.encode's result.
type Encoded = (n: int, state: u8)

// None is no scalar, a Codec's 0xFFFFFFFF; Bad is a malformed unit.
const None: u32 = 0xFFFFFFFF
const Bad: u32 = 0x110000

// One is a unit of n bytes that decodes to c, in state s.
One(n: int, c: u32, s: u8): Unit {
    return (n, c, c2: None, state: s)
}

// Short is the unit at b[i] when b ends inside it after have bytes: a wait
// for more input, or at the end one malformed unit of what is left.
Short(have: int, last: bool, s: u8): Unit {
    return if last { One(have, Bad, s) } else { One(0, Bad, s) }
}

// Restore is a malformed unit whose second byte y the standard restores to
// the input when it is ASCII, so the unit is the lead byte alone; else the
// unit is both bytes.
Restore(y: u8, s: u8): Unit {
    return One(if y < 0x80 { 1 } else { 2 }, Bad, s)
}

// Dec2 is the code point at pointer p of a two-byte table, 0 for none.
Dec2(t: []u8, p: int): u32 {
    if p < 0 || 2 * p + 1 >= t.Len() {
        return 0
    }
    return u32(t[2 * p]) | (u32(t[2 * p + 1]) << 8)
}

// Dec3 is the code point at pointer p of a three-byte table, 0 for none.
Dec3(t: []u8, p: int): u32 {
    if p < 0 || 3 * p + 2 >= t.Len() {
        return 0
    }
    return u32(t[3 * p]) | (u32(t[3 * p + 1]) << 8) | (u32(t[3 * p + 2]) << 16)
}

// Find is the pointer of code point c in a table of five-byte records
// sorted by code point (tables.mk's ...Enc), -1 for none: a binary search,
// O(log n).
Find(t: []u8, c: u32): int {
    var lo := 0
    var hi := t.Len() / 5
    for lo < hi {
        mid := lo + (hi - lo) / 2
        r := t[5 * mid..5 * mid + 5]
        k := u32(r[0]) | (u32(r[1]) << 8) | (u32(r[2]) << 16)
        if k < c {
            lo = mid + 1
        } else if k > c {
            hi = mid
        } else {
            return int(r[3]) | (int(r[4]) << 8)
        }
    }
    return -1
}

// Put1 and Put2 write one and two bytes to out and say so.
Put1(out: mut []u8, x: u32, s: u8): Encoded {
    out[0] = Trunc[u8](x)
    return (n: 1, state: s)
}

Put2(out: mut []u8, x: u32, y: u32, s: u8): Encoded {
    out[0] = Trunc[u8](x)
    out[1] = Trunc[u8](y)
    return (n: 2, state: s)
}

// NoBytes is an encoder's answer for a scalar the encoding has no bytes
// for.
NoBytes(s: u8): Encoded {
    return (n: -1, state: s)
}

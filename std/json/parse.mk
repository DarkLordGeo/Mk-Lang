// Why: programs read JSON from files and the network; parsing it must be
// exact, fast and bounded in depth.
//
// What: Parse, RFC 8259 text into a Value in the caller's arena, and the
// scanners format.mk shares: NextSpecial, Special16, Special and ScanNumber.
//
// How: recursive descent over the bytes of text, one pass, the
// grammar of RFC 8259 sections 2 to 7 exactly (no comments, no trailing
// commas, no leading zeros, no + sign, no bare control characters in a
// string), as serde_json's borrowed parser does it: a string without an
// escape is a view of text, so the only allocations are the List buffers of
// arrays and objects and the decoded copy of an escaped string. Depth is a
// counter: the 513th nested array or object fails with Depth at its opening
// bracket. Every error carries the byte offset of the first byte that cannot
// continue a JSON text (text.Len() when the text ends too soon).
//
// String scanning (NextSpecial) takes 16 bytes per step with std.simd: one
// vector, a mask of the bytes that are '"', '\' or below 0x20, the first
// by its trailing zeros, as simdjson's stage 1 classifies quotes,
// backslashes and control bytes (with 64-byte blocks); then one 8-byte
// SWAR word (Special) and bytes for the last 15. The vector is the
// baseline's 16 lanes, since std.json is not an unsafe module and so takes
// no `cpu` dispatch.

import std.core
import std.raw
import std.simd
import std.rt

// MaxDepth is the deepest nesting Parse accepts.
const MaxDepth = 512

// Cursor is where the parser is and how deep it is.
struct Cursor {
    i: int
    depth: int
}

/// Parses RFC 8259 text into a Value in a. The grammar is exact: no
/// comments, no trailing commas, no leading zeros, no + sign, no bare
/// control characters in a string. Fails with Syntax at the first byte that
/// cannot continue the text, Depth past 512 levels of nesting, OutOfMemory
/// from a. O(text); allocates the List buffers of arrays and objects and
/// the decoded copy of each escaped string in a, nothing for a string
/// without an escape. A string without an escape is a view of text, so text
/// is stored into a.
///
/// Each level of nesting is a recursive call. A dev build's frames are
/// larger, so on a task's default stack a dev build reaches the stack limit
/// (a panic) before 512 levels, while a release build fails with Depth
/// (measured in a dev-built server on x86-64 Windows: 400 levels parse, a
/// 512-deep body panics).
/// Parse untrusted text in a release build, or bound its depth first.
export Parse(a: &Arena, text: str into a): Error!Value from a, text {
    b := text.AsBytes()
    var p := Cursor(i: SkipWs(b, 0), depth: 0)
    v := try ParseValue(a, text, mut p)
    i := SkipWs(b, p.i)
    if i != b.Len() {
        fail Syntax(offset: i)
    }
    return v
}

// ParseValue parses the value at p.i, which follows any whitespace, and
// leaves p.i just after it.
ParseValue(a: &Arena, text: str into a, p: mut Cursor): Error!Value from a, text {
    b := text.AsBytes()
    i := p.i
    if i >= b.Len() {
        fail Syntax(offset: i)
    }
    c := b[i]
    if c == 0x22 {
        p.i = i + 1
        s := try ParseStr(a, text, p)
        return Value.Text(s: s)
    }
    if c == 0x7B {
        return try ParseObject(a, text, p)
    }
    if c == 0x5B {
        return try ParseArray(a, text, p)
    }
    if c == 0x2D || c >= 0x30 && c <= 0x39 {
        e := try ScanNumber(b, i)
        p.i = e
        return Value.Number(text: text[i..e])
    }
    if c == 0x74 {
        p.i = try Word(b, i, "true")
        return Value.Bool(b: true)
    }
    if c == 0x66 {
        p.i = try Word(b, i, "false")
        return Value.Bool(b: false)
    }
    if c == 0x6E {
        p.i = try Word(b, i, "null")
        return Value.Null
    }
    fail Syntax(offset: i)
}

// ParseArray: p.i is at '['.
ParseArray(a: &Arena, text: str into a, p: mut Cursor): Error!Value from a, text {
    b := text.AsBytes()
    n := b.Len()
    if p.depth == MaxDepth {
        fail Depth(offset: p.i)
    }
    p.depth += 1
    var items := List[Value].New(a)
    var i := SkipWs(b, p.i + 1)
    if i < n && b[i] == 0x5D {
        p.i = i + 1
        p.depth -= 1
        return Value.Array(items: items)
    }
    for {
        p.i = i
        v := try ParseValue(a, text, p)
        try items.Push(v)
        i = SkipWs(b, p.i)
        if i >= n {
            fail Syntax(offset: n)
        }
        c := b[i]
        if c == 0x2C {
            i = SkipWs(b, i + 1)
            continue
        }
        if c == 0x5D {
            break
        }
        fail Syntax(offset: i)
    }
    p.i = i + 1
    p.depth -= 1
    return Value.Array(items: items)
}

// ParseObject: p.i is at '{'. Members keep their order and duplicates.
ParseObject(a: &Arena, text: str into a, p: mut Cursor): Error!Value from a, text {
    b := text.AsBytes()
    n := b.Len()
    if p.depth == MaxDepth {
        fail Depth(offset: p.i)
    }
    p.depth += 1
    var members := List[Member].New(a)
    var i := SkipWs(b, p.i + 1)
    if i < n && b[i] == 0x7D {
        p.i = i + 1
        p.depth -= 1
        return Value.Object(members: members)
    }
    for {
        if i >= n {
            fail Syntax(offset: n)
        }
        if b[i] != 0x22 {
            fail Syntax(offset: i)
        }
        p.i = i + 1
        key := try ParseStr(a, text, p)
        i = SkipWs(b, p.i)
        if i >= n {
            fail Syntax(offset: n)
        }
        if b[i] != 0x3A {
            fail Syntax(offset: i)
        }
        p.i = SkipWs(b, i + 1)
        v := try ParseValue(a, text, p)
        try members.Push(Member(key: key, value: v))
        i = SkipWs(b, p.i)
        if i >= n {
            fail Syntax(offset: n)
        }
        c := b[i]
        if c == 0x2C {
            i = SkipWs(b, i + 1)
            continue
        }
        if c == 0x7D {
            break
        }
        fail Syntax(offset: i)
    }
    p.i = i + 1
    p.depth -= 1
    return Value.Object(members: members)
}

// ParseStr: p.i is just after the opening quote; on return it is just after
// the closing one. Without an escape the result is a view of text.
ParseStr(a: &Arena, text: str, p: mut Cursor): Error!str from a, text {
    b := text.AsBytes()
    n := b.Len()
    start := p.i
    i := NextSpecial(text, start)
    if i >= n {
        fail Syntax(offset: n)
    }
    c := b[i]
    if c == 0x22 {
        p.i = i + 1
        return text[start..i]
    }
    if c == 0x5C {
        return try Unescape(a, text, start, i, p)
    }
    fail Syntax(offset: i)
}

// NextSpecial is the index of the first byte of s at or after i0 that is
// '"', '\' or below 0x20, or s.Len(): 16 bytes a vector while 16 remain,
// then one SWAR word, then bytes. Each step's answer is exact, so it is
// the byte loop's (TestSTDVECTOR_JsonScanDiff). The scan runs past the
// string's end into the rest of the text, which is where the closing quote
// is found; a load never passes s.Len().
NextSpecial(s: str, i0: int): int {
    b := s.AsBytes()
    n := b.Len()
    var i := i0
    for i + 16 <= n {
        m := Special16(b, i)
        if m != 0 {
            return i + raw.Ctz64(m)
        }
        i += 16
    }
    if i + 8 <= n {
        m := Special(core.LoadU64(s, i))
        if m != 0 {
            return i + raw.Ctz64(m) / 8
        }
        i += 8
    }
    for i < n {
        c := b[i]
        if c == 0x22 || c == 0x5C || c < 0x20 {
            return i
        }
        i += 1
    }
    return n
}

// Special16 marks, in bit j, the bytes b[i + j] of the 16 from i that are
// '"', backslash or below 0x20 (unsigned): three compares of one vector.
Special16(b: []u8, i: int): u64 {
    v := simd.Vec[u8, 16].FromSlice(b, i)
    return ((v == simd.Vec[u8, 16].Splat(0x22)) | (v == simd.Vec[u8, 16].Splat(0x5C)) | (v < simd.Vec[u8, 16].Splat(0x20))).Bits()
}

// Special marks, in bit 7 of each byte, the bytes of w (8 bytes of text,
// little-endian) that are '"', '\' or below 0x20. The lowest mark is exact:
// a false mark (a borrow) appears only above a true one.
Special(w: u64): u64 {
    q := w ^ 0x2222222222222222
    s := w ^ 0x5C5C5C5C5C5C5C5C
    hq := (q -% 0x0101010101010101) & ~q
    hs := (s -% 0x0101010101010101) & ~s
    hc := (w -% 0x2020202020202020) & ~w
    return (hq | hs | hc) & 0x8080808080808080
}

// Unescape decodes the string that starts at start and has its first '\'
// at first. The first pass finds the closing quote and checks every byte
// on the way, escapes included (EscapeLen), the plain runs between escapes
// skipped by NextSpecial's vector steps; the second copies each run up to
// the next backslash, found by NextSpecial too. The first error is the one
// nearest the start: a bad escape is Syntax at its backslash even
// when a bare control byte or the end of the text comes after it. The copy
// is then one exact allocation of at most the raw length (every escape is
// longer than what it decodes to), and the second pass decodes into it; its
// own checks cannot fire after the first pass.
Unescape(a: &Arena, text: str, start: int, first: int, p: mut Cursor): Error!str from a, text {
    b := text.AsBytes()
    n := b.Len()
    var end := first
    for {
        if end >= n {
            fail Syntax(offset: n)
        }
        c := b[end]
        if c == 0x22 {
            break
        }
        if c != 0x5C {
            // NextSpecial stops only at '"', '\' or a control byte.
            fail Syntax(offset: end)
        }
        end = NextSpecial(text, end + try EscapeLen(b, end))
    }
    out := try a.Slice[u8](end - start)
    var k := first - start
    (mut out[0..k]).CopyFrom(b[start..first])
    var j := first
    for j < end {
        c := b[j]
        if c != 0x5C {
            // The next escape, 16 bytes a step: the first pass
            // checked every byte up to end, the closing quote, so the
            // next special byte is a backslash or end itself.
            r := NextSpecial(text, j + 1)
            (mut out[k..k + (r - j)]).CopyFrom(b[j..r])
            k += r - j
            j = r
            continue
        }
        e := b[j + 1]
        d: u8 = if e == 0x22 || e == 0x5C || e == 0x2F {
            e
        } else if e == 0x62 {
            0x08
        } else if e == 0x66 {
            0x0C
        } else if e == 0x6E {
            0x0A
        } else if e == 0x72 {
            0x0D
        } else if e == 0x74 {
            0x09
        } else if e == 0x75 {
            var cp := Hex4(b, j + 2, end)
            if cp < 0 {
                fail Syntax(offset: j)
            }
            var step := 6
            if cp >= 0xDC00 && cp <= 0xDFFF {
                fail Syntax(offset: j)
            }
            if cp >= 0xD800 && cp <= 0xDBFF {
                // A high surrogate needs a low one right after it.
                if j + 12 > end || b[j + 6] != 0x5C || b[j + 7] != 0x75 {
                    fail Syntax(offset: j)
                }
                lo := Hex4(b, j + 8, end)
                if lo < 0xDC00 || lo > 0xDFFF {
                    fail Syntax(offset: j)
                }
                cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00)
                step = 12
            }
            k += PutUtf8(out, k, cp)
            j += step
            continue
        } else {
            fail Syntax(offset: j)
        }
        out[k] = d
        k += 1
        j += 2
    }
    p.i = end + 1
    // The copy is valid UTF-8: its runs are slices of text between ASCII
    // bytes, and each escape is encoded as UTF-8 of a scalar (no surrogate).
    // rt.StrFrom checks it again (simd.ValidUtf8), one pass over the
    // copy, since std.json has no unsafe; it cannot fail.
    return rt.StrFrom(out[0..k]).Must()
}

// EscapeLen checks the escape whose backslash is at b[j] and returns its
// length in the text: 2, 6 for \uXXXX, or 12 for a surrogate pair. The
// text ending inside it is Syntax at b.Len(); any other fault (a letter
// that is no escape, a byte that is not a hex digit, a lone or reversed
// surrogate) is Syntax at the backslash, RFC 8259 section 7.
EscapeLen(b: []u8, j: int): Syntax!int {
    n := b.Len()
    if j + 1 >= n {
        fail Syntax(offset: n)
    }
    e := b[j + 1]
    if e == 0x22 || e == 0x5C || e == 0x2F || e == 0x62 || e == 0x66 || e == 0x6E || e == 0x72 || e == 0x74 {
        return 2
    }
    if e != 0x75 {
        fail Syntax(offset: j)
    }
    cp := Hex4(b, j + 2, n)
    if cp == -2 {
        fail Syntax(offset: n)
    }
    if cp < 0 || (cp >= 0xDC00 && cp <= 0xDFFF) {
        fail Syntax(offset: j)
    }
    if cp < 0xD800 || cp > 0xDBFF {
        return 6
    }
    // A high surrogate needs a low one right after it.
    if j + 6 >= n {
        fail Syntax(offset: n)
    }
    if b[j + 6] != 0x5C {
        fail Syntax(offset: j)
    }
    if j + 7 >= n {
        fail Syntax(offset: n)
    }
    if b[j + 7] != 0x75 {
        fail Syntax(offset: j)
    }
    lo := Hex4(b, j + 8, n)
    if lo == -2 {
        fail Syntax(offset: n)
    }
    if lo < 0xDC00 || lo > 0xDFFF {
        fail Syntax(offset: j)
    }
    return 12
}

// Hex4 is the value of the 4 hex digits at b[i..i + 4]; -1 when a byte
// before end among them is not a hex digit, -2 when end comes first and
// every byte before it is one.
Hex4(b: []u8, i: int, end: int): int {
    var v := 0
    for k in 0..4 {
        if i + k >= end {
            return -2
        }
        c := b[i + k]
        d := if c >= 0x30 && c <= 0x39 {
            Cast[int](c - 0x30)
        } else if (c | 0x20) >= 0x61 && (c | 0x20) <= 0x66 {
            Cast[int]((c | 0x20) - 0x61) + 10
        } else {
            return -1
        }
        v = v * 16 + d
    }
    return v
}

// PutUtf8 writes scalar cp (not a surrogate, at most 0x10FFFF) as UTF-8 at
// out[k..] and returns the byte count.
PutUtf8(out: mut []u8, k: int, cp: int): int {
    if cp < 0x80 {
        out[k] = Trunc[u8](cp)
        return 1
    }
    if cp < 0x800 {
        out[k] = Trunc[u8](0xC0 | (cp >> 6))
        out[k + 1] = Trunc[u8](0x80 | (cp & 0x3F))
        return 2
    }
    if cp < 0x10000 {
        out[k] = Trunc[u8](0xE0 | (cp >> 12))
        out[k + 1] = Trunc[u8](0x80 | ((cp >> 6) & 0x3F))
        out[k + 2] = Trunc[u8](0x80 | (cp & 0x3F))
        return 3
    }
    out[k] = Trunc[u8](0xF0 | (cp >> 18))
    out[k + 1] = Trunc[u8](0x80 | ((cp >> 12) & 0x3F))
    out[k + 2] = Trunc[u8](0x80 | ((cp >> 6) & 0x3F))
    out[k + 3] = Trunc[u8](0x80 | (cp & 0x3F))
    return 4
}

// ScanNumber returns the end of the number at b[i], RFC 8259 section 6:
// -? (0 | [1-9][0-9]*) (. [0-9]+)? ([eE] [+-]? [0-9]+)?
ScanNumber(b: []u8, i0: int): Syntax!int {
    n := b.Len()
    var i := i0
    if b[i] == 0x2D {
        i += 1
    }
    if i >= n {
        fail Syntax(offset: n)
    }
    if b[i] == 0x30 {
        i += 1
    } else if b[i] >= 0x31 && b[i] <= 0x39 {
        i = Digits(b, i + 1)
    } else {
        fail Syntax(offset: i)
    }
    if i < n && b[i] == 0x2E {
        i += 1
        if i >= n {
            fail Syntax(offset: n)
        }
        if b[i] < 0x30 || b[i] > 0x39 {
            fail Syntax(offset: i)
        }
        i = Digits(b, i + 1)
    }
    if i < n && (b[i] | 0x20) == 0x65 {
        i += 1
        if i < n && (b[i] == 0x2B || b[i] == 0x2D) {
            i += 1
        }
        if i >= n {
            fail Syntax(offset: n)
        }
        if b[i] < 0x30 || b[i] > 0x39 {
            fail Syntax(offset: i)
        }
        i = Digits(b, i + 1)
    }
    return i
}

// Digits is the end of the run of ASCII digits at b[i].
Digits(b: []u8, i: int): int {
    n := b.Len()
    var j := i
    for j < n && b[j] >= 0x30 && b[j] <= 0x39 {
        j += 1
    }
    return j
}

// Word checks that the literal w is at b[i] and returns its end; else
// Syntax at the first byte that differs.
Word(b: []u8, i: int, w: str): Syntax!int {
    n := b.Len()
    wb := w.AsBytes()
    for k in 0..wb.Len() {
        if i + k >= n {
            fail Syntax(offset: n)
        }
        if b[i + k] != wb[k] {
            fail Syntax(offset: i + k)
        }
    }
    return i + wb.Len()
}

// SkipWs is the first index at or after i that is not JSON whitespace
// (space, tab, line feed, carriage return).
SkipWs(b: []u8, i: int): int {
    n := b.Len()
    var j := i
    for j < n {
        c := b[j]
        if c != 0x20 && c != 0x0A && c != 0x0D && c != 0x09 {
            return j
        }
        j += 1
    }
    return j
}

// Why: keys, tokens and data URLs carry bytes as base64 text, and a strict
// decoder must accept exactly one text for each byte string.
//
// What: Alphabet and its values Base64, Base64Url, Base64Raw, Base64UrlRaw
// and Base64Forgiving (RFC 4648 sections 4 and 5, padded or not, and the
// WHATWG Infra Standard's forgiving-base64 decode); Base64Len,
// Base64EncodeTo, Base64Encode, Base64DecodeTo and Base64Decode.
//
// How: strict decoding is RFC 4648 section 3.5's canonical form: every byte
// is in the alphabet (no whitespace, no line breaks), padding is exactly
// what the length needs (none for the unpadded alphabets), and the unused
// bits of the last character are zero, so every accepted text decodes back
// to itself. The scalar loops take three bytes to four characters and back,
// the decode side through one 256-entry table whose invalid entries have
// bit 7 set, so four characters cost one test (as in Go's encoding/base64
// and Rust's base64 crate); base64_simd.mk takes every whole block past 32
// bytes first. The forgiving decode is used only when the caller names
// Base64Forgiving.

import std.rt
import std.simd

/// Alphabet is a base64 alphabet and its padding rule: Base64, Base64Url,
/// Base64Raw, Base64UrlRaw or Base64Forgiving.
export struct Alphabet {
    url: bool
    pad: bool
    forgiving: bool
}

/// Base64 is RFC 4648 section 4: `+` and `/`, padded with `=`.
export const Base64: Alphabet = Alphabet(url: false, pad: true, forgiving: false)

/// Base64Url is RFC 4648 section 5: `-` and `_`, padded with `=`.
export const Base64Url: Alphabet = Alphabet(url: true, pad: true, forgiving: false)

/// Base64Raw is Base64 with no padding.
export const Base64Raw: Alphabet = Alphabet(url: false, pad: false, forgiving: false)

/// Base64UrlRaw is Base64Url with no padding.
export const Base64UrlRaw: Alphabet = Alphabet(url: true, pad: false, forgiving: false)

/// Base64Forgiving encodes as Base64 and decodes by the WHATWG Infra
/// Standard's forgiving-base64 decode: ASCII whitespace is skipped, padding
/// may be left out, and the unused bits of the last character are ignored.
export const Base64Forgiving: Alphabet = Alphabet(url: false, pad: true, forgiving: true)

const StdChars: str = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
const UrlChars: str = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// EncTable is the 64 characters of chars as bytes.
EncTable(chars: str): [64]u8 {
    var t: [64]u8 = [0; 64]
    for i in 0..64 {
        t[i] = chars.Byte(i)
    }
    return t
}

// DecTable maps each byte to its 6-bit value, and every byte outside chars
// to 0xFF, so an OR of four entries has bit 7 set exactly when one is bad.
DecTable(chars: str): [256]u8 {
    var t: [256]u8 = [0xFF; 256]
    for i in 0..64 {
        t[int(chars.Byte(i))] = Trunc[u8](i)
    }
    return t
}

const StdEnc: [64]u8 = EncTable(StdChars)
const UrlEnc: [64]u8 = EncTable(UrlChars)
const StdDec: [256]u8 = DecTable(StdChars)
const UrlDec: [256]u8 = DecTable(UrlChars)

/// Base64Len is the length of the encoding of n bytes under alphabet:
/// 4 * ceil(n / 3) when padded, ceil(4 * n / 3) when not. O(1); allocates
/// nothing. n below 0 panics, and so does an n whose encoding's length
/// overflows int.
export Base64Len(n: int, alphabet: Alphabet): int {
    Assert(n >= 0)
    full := n / 3 * 4
    rest := n % 3
    if rest == 0 {
        return full
    }
    return if alphabet.pad { full + 4 } else { full + rest + 1 }
}

/// Base64EncodeTo writes the encoding of data to the front of dst and
/// returns its length, Base64Len(data.Len(), alphabet). O(n); allocates
/// nothing. A dst shorter than that panics: its length is the caller's.
export Base64EncodeTo(dst: mut []u8, data: []u8, alphabet: Alphabet): int {
    n := data.Len()
    total := Base64Len(n, alphabet)
    Assert(dst.Len() >= total)
    enc := if alphabet.url { &UrlEnc } else { &StdEnc }
    var i := 0
    var o := 0
    if n >= 32 {
        (read, wrote) := Base64EncodeVec(mut dst[..], data, alphabet.url)
        i = read
        o = wrote
    }
    for n - i >= 3 {
        w := data[i..i + 3]
        x := (u32(w[0]) << 16) | (u32(w[1]) << 8) | u32(w[2])
        var d := mut dst[o..o + 4]
        d[0] = enc[int(x >> 18)]
        d[1] = enc[int((x >> 12) & 63)]
        d[2] = enc[int((x >> 6) & 63)]
        d[3] = enc[int(x & 63)]
        i += 3
        o += 4
    }
    rest := n - i
    if rest == 0 {
        return o
    }
    var x := u32(data[i]) << 16
    if rest == 2 {
        x = x | (u32(data[i + 1]) << 8)
    }
    dst[o] = enc[int(x >> 18)]
    dst[o + 1] = enc[int((x >> 12) & 63)]
    o += 2
    if rest == 2 {
        dst[o] = enc[int((x >> 6) & 63)]
        o += 1
    }
    if alphabet.pad {
        for o < total {
            dst[o] = b'='
            o += 1
        }
    }
    return o
}

/// Base64Encode is the encoding of data under alphabet, in a. O(n);
/// allocates Base64Len(data.Len(), alphabet) bytes in a.
export Base64Encode(a: &Arena, data: []u8, alphabet: Alphabet): OutOfMemory!str from a {
    out := try a.Slice[u8](Base64Len(data.Len(), alphabet))
    _ = Base64EncodeTo(mut out[..], data, alphabet)
    // Must: every byte written is one of the alphabet's ASCII characters
    // or `=`, which is valid UTF-8.
    return rt.StrFrom(out[..]).Must()
}

// Shape is the work of one strict decode: data is the length of s without
// its padding, out the decoded length.
type Shape = (data: int, out: int)

// StrictShape counts the trailing padding of a strict alphabet (at most two
// `=`, only where padding is allowed) and the decoded length it implies. It
// reads at most two bytes and judges nothing else.
StrictShape(b: []u8, pad: bool): Shape {
    n := b.Len()
    var p := 0
    if pad && n >= 1 && b[n - 1] == b'=' {
        p = 1
        if n >= 2 && b[n - 2] == b'=' {
            p = 2
        }
    }
    m := n - p
    rem := m % 4
    tail := if rem == 0 { 0 } else { rem - 1 }
    return (data: m, out: m / 4 * 3 + tail)
}

// IsSpace is WHATWG Infra's ASCII whitespace: TAB, LF, FF, CR and SPACE.
IsSpace(c: u8): bool {
    return c == 0x20 || c == 0x09 || c == 0x0A || c == 0x0C || c == 0x0D
}

// ForgivingShape is the forgiving decode's shape: data counts the
// characters that are not whitespace or final padding, out the decoded
// length. Padding is one or two `=` at the end of a text whose non-space
// length is a multiple of 4 (forgiving-base64 decode, steps 1 and 2).
// Whitespace is counted 16 bytes a vector step; the last two characters
// that are not whitespace are found from the end.
ForgivingShape(b: []u8): Shape {
    n := b.Len()
    var spaces := 0
    var i := 0
    if n >= 16 {
        sp := simd.Vec[u8, 16].Splat(0x20)
        tab := simd.Vec[u8, 16].Splat(0x09)
        cr := simd.Vec[u8, 16].Splat(0x0D)
        vt := simd.Vec[u8, 16].Splat(0x0B)
        for n - i >= 16 {
            v := simd.Vec[u8, 16].FromSlice(b, i)
            spaces += ((v == sp) | ((v >= tab) & (v <= cr) & (v != vt))).CountSet()
            i += 16
        }
    }
    for x in b[i..n] {
        if IsSpace(x) {
            spaces += 1
        }
    }
    count := n - spaces
    var last := n - 1
    for last >= 0 && IsSpace(b[last]) {
        last -= 1
    }
    var p := 0
    if count % 4 == 0 && last >= 0 && b[last] == b'=' {
        p = 1
        var prev := last - 1
        for prev >= 0 && IsSpace(b[prev]) {
            prev -= 1
        }
        if prev >= 0 && b[prev] == b'=' {
            p = 2
        }
    }
    m := count - p
    rem := m % 4
    tail := if rem == 0 { 0 } else { rem - 1 }
    return (data: m, out: m / 4 * 3 + tail)
}

// BadIn is the offset of the first byte of b[from..to] whose entry in dec
// is invalid; to when there is none.
BadIn(b: []u8, from: int, to: int, dec: &[256]u8): int {
    var i := from
    for i < to && dec[int(b[i])] < 0x80 {
        i += 1
    }
    return i
}

// StrictTo decodes the data characters b[0..shape.data] of a strict
// alphabet into dst, which holds shape.out bytes, and checks the length and
// the last character's unused bits. Errors in offset order: the first bad
// character, then a length that no encoding has, then unused bits that are
// not zero.
StrictTo(dst: mut []u8, b: []u8, shape: Shape, alphabet: Alphabet): Error!void {
    dec := if alphabet.url { &UrlDec } else { &StdDec }
    m := shape.data
    full := m / 4 * 4
    var i := 0
    var o := 0
    if full >= 32 {
        (read, wrote) := Base64DecodeVec(mut dst[..], b[0..full], alphabet.url)
        i = read
        o = wrote
    }
    for i < full {
        w := b[i..i + 4]
        c0 := dec[int(w[0])]
        c1 := dec[int(w[1])]
        c2 := dec[int(w[2])]
        c3 := dec[int(w[3])]
        if (c0 | c1 | c2 | c3) >= 0x80 {
            fail BadInput(offset: BadIn(b, i, i + 4, dec))
        }
        x := (u32(c0) << 18) | (u32(c1) << 12) | (u32(c2) << 6) | u32(c3)
        var d := mut dst[o..o + 3]
        d[0] = Trunc[u8](x >> 16)
        d[1] = Trunc[u8](x >> 8)
        d[2] = Trunc[u8](x)
        i += 4
        o += 3
    }
    bad := BadIn(b, full, m, dec)
    if bad < m {
        fail BadInput(offset: bad)
    }
    n := b.Len()
    rem := m - full
    if alphabet.pad && n % 4 != 0 || rem == 1 {
        fail BadInput(offset: n)
    }
    try TailTo(mut dst[o..], b[full..m], dec, true, full)
}

// TailTo decodes the last two or three characters t of a text (already
// checked to be in the alphabet) into dst. With strict, unused bits of the
// last character that are not zero fail at its offset, base + t.Len() - 1.
TailTo(dst: mut []u8, t: []u8, dec: &[256]u8, strict: bool, base: int): Error!void {
    k := t.Len()
    if k == 0 {
        return
    }
    var x := (u32(dec[int(t[0])]) << 18) | (u32(dec[int(t[1])]) << 12)
    if k == 3 {
        x = x | (u32(dec[int(t[2])]) << 6)
    }
    unused := if k == 2 { x & 0xFFFF } else { x & 0xFF }
    if strict && unused != 0 {
        fail BadInput(offset: base + k - 1)
    }
    dst[0] = Trunc[u8](x >> 16)
    if k == 3 {
        dst[1] = Trunc[u8](x >> 8)
    }
}

// ForgivingTo decodes b by forgiving-base64 decode into dst, which holds
// shape.out bytes: the first shape.data characters that are not whitespace
// are data, and the rest is whitespace and the padding ForgivingShape
// found. Characters are gathered four at a time across whitespace. At a
// quad's start, at the text's start and after whitespace, the strict
// vector step takes every whole 16-character block it can: it refuses a
// block that holds whitespace, padding or a byte outside the alphabet, so
// what it takes is data, and the scalar loop meets every error first.
ForgivingTo(dst: mut []u8, b: []u8, shape: Shape): Error!void {
    dec := &StdDec
    m := shape.data
    var seen := 0
    var i := 0
    var o := 0
    var q: [4]u8 = [0; 4]
    var at: [4]int = [0; 4]
    var k := 0
    var vec := true
    for seen < m {
        if vec && k == 0 && m - seen >= 32 {
            vec = false
            (read, wrote) := Base64DecodeVec(mut dst[o..], b[i..], false)
            i += read
            o += wrote
            seen += read
            if seen >= m {
                break
            }
        }
        c := b[i]
        if IsSpace(c) {
            vec = true
            i += 1
            continue
        }
        if dec[int(c)] >= 0x80 {
            fail BadInput(offset: i)
        }
        q[k] = c
        at[k] = i
        k += 1
        seen += 1
        i += 1
        if k == 4 {
            x := (u32(dec[int(q[0])]) << 18) | (u32(dec[int(q[1])]) << 12) | (u32(dec[int(q[2])]) << 6) | u32(dec[int(q[3])])
            var d := mut dst[o..o + 3]
            d[0] = Trunc[u8](x >> 16)
            d[1] = Trunc[u8](x >> 8)
            d[2] = Trunc[u8](x)
            o += 3
            k = 0
        }
    }
    if k == 1 {
        fail BadInput(offset: b.Len())
    }
    try TailTo(mut dst[o..], q[0..k], dec, false, at[0])
}

/// Base64DecodeTo decodes s under alphabet into the front of dst and
/// returns the number of bytes written. Fails with TooLarge when dst is
/// shorter than the decoded length, which is checked first, and with
/// BadInput at the first byte outside the alphabet, at s.Len() when the
/// length is not one an encoding has (padding missing, or one character
/// left over), or at the last character when its unused bits are not zero;
/// Base64Forgiving skips whitespace and checks neither padding nor unused
/// bits. On an error dst's contents are unspecified. O(n); allocates
/// nothing.
export Base64DecodeTo(dst: mut []u8, s: str, alphabet: Alphabet): Error!int {
    b := s.AsBytes()
    if alphabet.forgiving {
        shape := ForgivingShape(b)
        if dst.Len() < shape.out {
            fail TooLarge
        }
        // Only the decoded length: a vector step stores 16 bytes for 12.
        try ForgivingTo(mut dst[0..shape.out], b, shape)
        return shape.out
    }
    shape := StrictShape(b, alphabet.pad)
    if dst.Len() < shape.out {
        fail TooLarge
    }
    // Only the decoded length: a vector step stores 16 bytes for 12, and
    // nothing past the count returned may change.
    try StrictTo(mut dst[0..shape.out], b, shape, alphabet)
    return shape.out
}

/// Base64Decode is the bytes s encodes under alphabet, in a. Fails with
/// BadInput as Base64DecodeTo does. O(n); allocates the decoded length in a
/// (at most 3 bytes for every 4 of s), also when the decode then fails.
export Base64Decode(a: &Arena, s: str, alphabet: Alphabet): Error|OutOfMemory![]u8 from a {
    b := s.AsBytes()
    shape := if alphabet.forgiving { ForgivingShape(b) } else { StrictShape(b, alphabet.pad) }
    out := try a.Slice[u8](shape.out)
    if alphabet.forgiving {
        try ForgivingTo(mut out[..], b, shape)
    } else {
        try StrictTo(mut out[..], b, shape, alphabet)
    }
    return out[..]
}

// Why: Japanese text still arrives in EUC-JP, ISO-2022-JP and Shift_JIS,
// and must decode exactly as the WHATWG Encoding Standard says.
//
// What: EucJpDecode, EucJpEncode, Iso2022JpDecode, Iso2022JpEncode,
// ShiftJisDecode and ShiftJisEncode, and ISO-2022-JP's decoder and encoder
// states (JpAscii, JpRoman, JpKatakana, JpLead, JpEscape and the JpEnc
// states).
//
// How: each handler follows the standard's algorithm and reads the jis0208
// and jis0212 indexes of tables.mk; ISO-2022-JP keeps its mode in the
// Codec's one byte of state, so a Decoder carries it between chunks.

// EucJpDecode is EUC-JP's decoder handler: ASCII, 0x8E and a byte as
// halfwidth katakana, 0x8F and two bytes through index jis0212, and two
// bytes 0xA1 to 0xFE through index jis0208.
EucJpDecode(b: []u8, i: int, last: bool, s: u8): Unit {
    x := b[i]
    if x < 0x80 {
        return One(1, u32(x), s)
    }
    if x != 0x8E && x != 0x8F && (x < 0xA1 || x == 0xFF) {
        return One(1, Bad, s)
    }
    rem := b.Len() - i
    if rem < 2 {
        return Short(rem, last, s)
    }
    y := b[i + 1]
    if x == 0x8E && y >= 0xA1 && y <= 0xDF {
        return One(2, 0xFF61 - 0xA1 + u32(y), s)
    }
    if x == 0x8F && y >= 0xA1 && y <= 0xFE {
        if rem < 3 {
            return Short(rem, last, s)
        }
        z := b[i + 2]
        var cp: u32 = 0
        if z >= 0xA1 && z <= 0xFE {
            cp = Dec2(Jis0212Dec, (int(y) - 0xA1) * 94 + int(z) - 0xA1)
        }
        if cp != 0 {
            return One(3, cp, s)
        }
        return One(if z < 0x80 { 2 } else { 3 }, Bad, s)
    }
    var cp: u32 = 0
    if x >= 0xA1 && x <= 0xFE && y >= 0xA1 && y <= 0xFE {
        cp = Dec2(Jis0208Dec, (int(x) - 0xA1) * 94 + int(y) - 0xA1)
    }
    if cp != 0 {
        return One(2, cp, s)
    }
    return Restore(y, s)
}

// EucJpEncode is EUC-JP's encoder handler: ASCII, U+00A5 and U+203E as
// 0x5C and 0x7E, halfwidth katakana after 0x8E, and index jis0208's index
// pointer, with U+2212 as U+FF0D.
EucJpEncode(c: u32, s: u8, out: mut []u8): Encoded {
    if c == None {
        return (n: 0, state: s)
    }
    if c < 0x80 {
        return Put1(out, c, s)
    }
    if c == 0xA5 {
        return Put1(out, 0x5C, s)
    }
    if c == 0x203E {
        return Put1(out, 0x7E, s)
    }
    if c >= 0xFF61 && c <= 0xFF9F {
        return Put2(out, 0x8E, c - 0xFF61 + 0xA1, s)
    }
    p := Find(Jis0208Enc, if c == 0x2212 { 0xFF0D } else { c })
    if p < 0 {
        return NoBytes(s)
    }
    return Put2(out, Trunc[u32](p / 94 + 0xA1), Trunc[u32](p % 94 + 0xA1), s)
}

// ISO-2022-JP's decoder state between units is its output state and its
// output flag: the mode in the low two bits, the flag in bit 2. Its
// decoder state proper equals the output state at every unit's start.
const JpAscii: u8 = 0
const JpRoman: u8 = 1
const JpKatakana: u8 = 2
const JpLead: u8 = 3
const JpOutput: u8 = 4

// Iso2022JpDecode is ISO-2022-JP's decoder handler, a unit at a time: an
// escape sequence of three bytes changes the mode and decodes to nothing,
// or to U+FFFD when it follows another with nothing between; a broken
// escape is malformed at its 0x1B alone, the rest read again in the mode
// before it; in the two-byte mode a pair is one unit through index
// jis0208.
Iso2022JpDecode(b: []u8, i: int, last: bool, s: u8): Unit {
    mode := s & 3
    x := b[i]
    rem := b.Len() - i
    if x == 0x1B {
        if rem < 2 {
            return Short(rem, last, mode)
        }
        y := b[i + 1]
        if y != 0x24 && y != 0x28 {
            return One(1, Bad, mode)
        }
        if rem < 3 {
            // At the end the standard restores the 0x24 or 0x28 alone.
            return if last { One(1, Bad, mode) } else { One(0, Bad, s) }
        }
        z := b[i + 2]
        var to: u8 = 255
        if y == 0x28 && z == 0x42 {
            to = JpAscii
        } else if y == 0x28 && z == 0x4A {
            to = JpRoman
        } else if y == 0x28 && z == 0x49 {
            to = JpKatakana
        } else if y == 0x24 && (z == 0x40 || z == 0x42) {
            to = JpLead
        }
        if to == 255 {
            return One(1, Bad, mode)
        }
        return One(3, if s & JpOutput != 0 { Bad } else { None }, to | JpOutput)
    }
    if mode == JpLead {
        if x < 0x21 || x > 0x7E {
            return One(1, Bad, mode)
        }
        if rem < 2 {
            return Short(rem, last, mode)
        }
        y := b[i + 1]
        if y == 0x1B {
            // The trail byte opens an escape: the lead is malformed and the
            // escape is read next.
            return One(1, Bad, mode)
        }
        if y < 0x21 || y > 0x7E {
            return One(2, Bad, mode)
        }
        cp := Dec2(Jis0208Dec, (int(x) - 0x21) * 94 + int(y) - 0x21)
        return One(2, if cp == 0 { Bad } else { cp }, mode)
    }
    if mode == JpKatakana {
        return One(1, if x >= 0x21 && x <= 0x5F { 0xFF61 - 0x21 + u32(x) } else { Bad }, mode)
    }
    if x >= 0x80 || x == 0x0E || x == 0x0F {
        return One(1, Bad, mode)
    }
    if mode == JpRoman && x == 0x5C {
        return One(1, 0xA5, mode)
    }
    if mode == JpRoman && x == 0x7E {
        return One(1, 0x203E, mode)
    }
    return One(1, u32(x), mode)
}

// ISO-2022-JP's encoder states.
const JpEncAscii: u8 = 0
const JpEncRoman: u8 = 1
const JpEncJis: u8 = 2

// JpEscape writes the escape sequence 0x1B a z and then the byte (or two)
// of the scalar in the new state, and says so.
JpEscape(out: mut []u8, a: u8, z: u8, x: u32, y: u32, two: bool, to: u8): Encoded {
    out[0] = 0x1B
    out[1] = a
    out[2] = z
    out[3] = Trunc[u8](x)
    if two {
        out[4] = Trunc[u8](y)
        return (n: 5, state: to)
    }
    return (n: 4, state: to)
}

// Iso2022JpEncode is ISO-2022-JP's encoder handler: the standard restores
// a scalar after a change of state and handles it again in the new state,
// which here is one call that writes the escape and the scalar's bytes.
Iso2022JpEncode(c: u32, s: u8, out: mut []u8): Encoded {
    if c == None {
        if s != JpEncAscii {
            out[0] = 0x1B
            out[1] = 0x28
            out[2] = 0x42
            return (n: 3, state: JpEncAscii)
        }
        return (n: 0, state: s)
    }
    if c == 0x0E || c == 0x0F || c == 0x1B {
        // In ASCII or Roman an error; elsewhere a change to ASCII and then
        // the same error.
        return NoBytes(s)
    }
    if s == JpEncAscii && c < 0x80 {
        return Put1(out, c, s)
    }
    if s == JpEncRoman && ((c < 0x80 && c != 0x5C && c != 0x7E) || c == 0xA5 || c == 0x203E) {
        return Put1(out, if c == 0xA5 { 0x5C } else if c == 0x203E { 0x7E } else { c }, s)
    }
    if c < 0x80 {
        return JpEscape(out, 0x28, 0x42, c, 0, false, JpEncAscii)
    }
    if c == 0xA5 || c == 0x203E {
        return JpEscape(out, 0x28, 0x4A, if c == 0xA5 { 0x5C } else { 0x7E }, 0, false, JpEncRoman)
    }
    var k := if c == 0x2212 { 0xFF0D } else { c }
    if k >= 0xFF61 && k <= 0xFF9F {
        k = u32(Katakana[int(k - 0xFF61)])
    }
    p := Find(Jis0208Enc, k)
    if p < 0 {
        return NoBytes(s)
    }
    lead := Trunc[u32](p / 94 + 0x21)
    trail := Trunc[u32](p % 94 + 0x21)
    if s != JpEncJis {
        return JpEscape(out, 0x24, 0x42, lead, trail, true, JpEncJis)
    }
    return Put2(out, lead, trail, s)
}

// ShiftJisDecode is Shift_JIS's decoder handler: ASCII and 0x80, 0xA1 to
// 0xDF as halfwidth katakana, and a lead and a trail byte through index
// jis0208, pointers 8836 to 10715 onto U+E000 to U+E757 (EUDC).
ShiftJisDecode(b: []u8, i: int, last: bool, s: u8): Unit {
    x := b[i]
    if x <= 0x80 {
        return One(1, u32(x), s)
    }
    if x >= 0xA1 && x <= 0xDF {
        return One(1, 0xFF61 - 0xA1 + u32(x), s)
    }
    if x == 0xA0 || x > 0xFC {
        return One(1, Bad, s)
    }
    rem := b.Len() - i
    if rem < 2 {
        return Short(rem, last, s)
    }
    y := b[i + 1]
    var p := -1
    off := if y < 0x7F { 0x40 } else { 0x41 }
    loff := if x < 0xA0 { 0x81 } else { 0xC1 }
    if (y >= 0x40 && y <= 0x7E) || (y >= 0x80 && y <= 0xFC) {
        p = (int(x) - loff) * 188 + int(y) - off
    }
    if p >= 8836 && p <= 10715 {
        return One(2, Trunc[u32](0xE000 - 8836 + p), s)
    }
    cp := Dec2(Jis0208Dec, p)
    if cp != 0 {
        return One(2, cp, s)
    }
    return Restore(y, s)
}

// ShiftJisEncode is Shift_JIS's encoder handler: ASCII and U+0080, U+00A5
// and U+203E as 0x5C and 0x7E, halfwidth katakana as one byte, and index
// Shift_JIS pointer, with U+2212 as U+FF0D.
ShiftJisEncode(c: u32, s: u8, out: mut []u8): Encoded {
    if c == None {
        return (n: 0, state: s)
    }
    if c <= 0x80 {
        return Put1(out, c, s)
    }
    if c == 0xA5 {
        return Put1(out, 0x5C, s)
    }
    if c == 0x203E {
        return Put1(out, 0x7E, s)
    }
    if c >= 0xFF61 && c <= 0xFF9F {
        return Put1(out, c - 0xFF61 + 0xA1, s)
    }
    k := if c == 0x2212 { 0xFF0D } else { c }
    var p := Find(SjisEnc, k)
    if p == 0xFFFF {
        return NoBytes(s)
    }
    if p < 0 {
        p = Find(Jis0208Enc, k)
    }
    if p < 0 {
        return NoBytes(s)
    }
    lead := p / 188
    loff := if lead < 0x1F { 0x81 } else { 0xC1 }
    trail := p % 188
    off := if trail < 0x3F { 0x40 } else { 0x41 }
    return Put2(out, Trunc[u32](lead + loff), Trunc[u32](trail + off), s)
}

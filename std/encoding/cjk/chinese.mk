// Why: Chinese web pages and files still arrive in gb18030, GBK and Big5,
// and a browser-grade decoder must read them exactly as the WHATWG Encoding
// Standard does.
//
// What: the decoder and encoder handlers of those encodings:
// Gb18030Decode (shared by GBK), Gb18030Encode, GbkEncode, Big5Decode and
// Big5Encode, with RangesCodePoint and RangesPointer for gb18030's
// four-byte ranges.
//
// How: each handler follows the standard's algorithm step by step and reads
// the generated indexes in tables.mk. A four-byte gb18030 form broken at
// its third or fourth byte is malformed at its first byte alone, and the
// rest is read again.

// Gb18030Decode is gb18030's decoder handler: ASCII, 0x80 as U+20AC, a
// two-byte form through index gb18030, and a four-byte form through index
// gb18030 ranges. A four-byte form broken at its third or fourth byte is
// malformed at its first byte alone, the rest read again.
Gb18030Decode(b: []u8, i: int, last: bool, s: u8): Unit {
    x := b[i]
    if x < 0x80 {
        return One(1, u32(x), s)
    }
    if x == 0x80 {
        return One(1, 0x20AC, s)
    }
    if x == 0xFF {
        return One(1, Bad, s)
    }
    rem := b.Len() - i
    if rem < 2 {
        return Short(rem, last, s)
    }
    y := b[i + 1]
    if y >= 0x30 && y <= 0x39 {
        if rem < 3 {
            return Short(rem, last, s)
        }
        z := b[i + 2]
        if z < 0x81 || z > 0xFE {
            return One(1, Bad, s)
        }
        if rem < 4 {
            return Short(rem, last, s)
        }
        w := b[i + 3]
        if w < 0x30 || w > 0x39 {
            return One(1, Bad, s)
        }
        p := (int(x) - 0x81) * 12600 + (int(y) - 0x30) * 1260 + (int(z) - 0x81) * 10 + int(w) - 0x30
        return One(4, RangesCodePoint(p), s)
    }
    var p := -1
    off := if y < 0x7F { 0x40 } else { 0x41 }
    if (y >= 0x40 && y <= 0x7E) || (y >= 0x80 && y <= 0xFE) {
        p = (int(x) - 0x81) * 190 + int(y) - off
    }
    cp := Dec2(Gb18030Dec, p)
    if cp != 0 {
        return One(2, cp, s)
    }
    return Restore(y, s)
}

// RangesCodePoint is index gb18030 ranges code point for pointer p, Bad
// for null.
RangesCodePoint(p: int): u32 {
    if (p > 39419 && p < 189000) || p > 1237575 {
        return Bad
    }
    if p == 7457 {
        return 0xE7C7
    }
    // The last range whose pointer is at most p; RangePtr[0] is 0.
    var lo := 0
    var hi := RangePtr.Len()
    for hi - lo > 1 {
        mid := lo + (hi - lo) / 2
        if int(RangePtr[mid]) <= p {
            lo = mid
        } else {
            hi = mid
        }
    }
    return RangeCp[lo] + Trunc[u32](p - int(RangePtr[lo]))
}

// RangesPointer is index gb18030 ranges pointer for code point c, which is
// U+0080 or above.
RangesPointer(c: u32): int {
    if c == 0xE7C7 {
        return 7457
    }
    var lo := 0
    var hi := RangeCp.Len()
    for hi - lo > 1 {
        mid := lo + (hi - lo) / 2
        if RangeCp[mid] <= c {
            lo = mid
        } else {
            hi = mid
        }
    }
    return int(RangePtr[lo]) + int(c - RangeCp[lo])
}

// GbEncode is gb18030's encoder handler, with is GBK as gbk.
GbEncode(c: u32, s: u8, out: mut []u8, gbk: bool): Encoded {
    if c == None {
        return (n: 0, state: s)
    }
    if c < 0x80 {
        return Put1(out, c, s)
    }
    if c == 0xE5E5 {
        return NoBytes(s)
    }
    if gbk && c == 0x20AC {
        return Put1(out, 0x80, s)
    }
    for row in Gb18030Side {
        if row >> 16 == c {
            return Put2(out, (row >> 8) & 0xFF, row & 0xFF, s)
        }
    }
    p := Find(Gb18030Enc, c)
    if p >= 0 {
        trail := p % 190
        off := if trail < 0x3F { 0x40 } else { 0x41 }
        return Put2(out, Trunc[u32](p / 190 + 0x81), Trunc[u32](trail + off), s)
    }
    if gbk {
        return NoBytes(s)
    }
    var q := RangesPointer(c)
    b1 := q / 12600
    q = q % 12600
    b2 := q / 1260
    q = q % 1260
    out[0] = Trunc[u8](b1 + 0x81)
    out[1] = Trunc[u8](b2 + 0x30)
    out[2] = Trunc[u8](q / 10 + 0x81)
    out[3] = Trunc[u8](q % 10 + 0x30)
    return (n: 4, state: s)
}

Gb18030Encode(c: u32, s: u8, out: mut []u8): Encoded {
    return GbEncode(c, s, out, false)
}

GbkEncode(c: u32, s: u8, out: mut []u8): Encoded {
    return GbEncode(c, s, out, true)
}

// Big5Decode is Big5's decoder handler: ASCII, and a lead byte 0x81 to
// 0xFE with a trail byte through index Big5, four pointers of which decode
// to two code points.
Big5Decode(b: []u8, i: int, last: bool, s: u8): Unit {
    x := b[i]
    if x < 0x80 {
        return One(1, u32(x), s)
    }
    if x == 0x80 || x == 0xFF {
        return One(1, Bad, s)
    }
    rem := b.Len() - i
    if rem < 2 {
        return Short(rem, last, s)
    }
    y := b[i + 1]
    var p := -1
    off := if y < 0x7F { 0x40 } else { 0x62 }
    if (y >= 0x40 && y <= 0x7E) || (y >= 0xA1 && y <= 0xFE) {
        p = (int(x) - 0x81) * 157 + int(y) - off
    }
    var k := 0
    for k < Big5Pairs.Len() {
        if int(Big5Pairs[k]) == p {
            return (n: 2, c: Big5Pairs[k + 1], c2: Big5Pairs[k + 2], state: s)
        }
        k += 3
    }
    cp := Dec3(Big5Dec, p)
    if cp != 0 {
        return One(2, cp, s)
    }
    return Restore(y, s)
}

// Big5Encode is Big5's encoder handler, through index Big5 pointer.
Big5Encode(c: u32, s: u8, out: mut []u8): Encoded {
    if c == None {
        return (n: 0, state: s)
    }
    if c < 0x80 {
        return Put1(out, c, s)
    }
    p := Find(Big5Enc, c)
    if p < 0 {
        return NoBytes(s)
    }
    trail := p % 157
    off := if trail < 0x3F { 0x40 } else { 0x62 }
    return Put2(out, Trunc[u32](p / 157 + 0x81), Trunc[u32](trail + off), s)
}

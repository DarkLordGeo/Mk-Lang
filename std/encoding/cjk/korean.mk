// Why: Korean text still arrives in EUC-KR, which the WHATWG Encoding
// Standard defines over its EUC-KR index.
// What: EucKrDecode and EucKrEncode, the encoding's two handlers.
// How: a lead byte 0x81 to 0xFE with a trail byte 0x41 to 0xFE is a pointer
// into EucKrDec; the encoder searches EucKrEnc.

// EucKrDecode is EUC-KR's decoder handler: ASCII, and a lead byte 0x81 to
// 0xFE with a trail byte 0x41 to 0xFE through index EUC-KR.
EucKrDecode(b: []u8, i: int, last: bool, s: u8): Unit {
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
    var cp: u32 = 0
    if y >= 0x41 && y <= 0xFE {
        cp = Dec2(EucKrDec, (int(x) - 0x81) * 190 + int(y) - 0x41)
    }
    if cp != 0 {
        return One(2, cp, s)
    }
    return Restore(y, s)
}

// EucKrEncode is EUC-KR's encoder handler, through index EUC-KR's index
// pointer.
EucKrEncode(c: u32, s: u8, out: mut []u8): Encoded {
    if c == None {
        return (n: 0, state: s)
    }
    if c < 0x80 {
        return Put1(out, c, s)
    }
    p := Find(EucKrEnc, c)
    if p < 0 {
        return NoBytes(s)
    }
    return Put2(out, Trunc[u32](p / 190 + 0x81), Trunc[u32](p % 190 + 0x41), s)
}

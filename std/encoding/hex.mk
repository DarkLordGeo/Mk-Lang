// Why: hashes, keys and dumps are printed and read as hex, RFC 4648
// section 8's base 16.
//
// What: HexEncode and HexDecode, with the tables HexDigits, HexTable and
// HexDec.
//
// How: encoding writes lower case, as Go's encoding/hex does; decoding
// takes either case. Each byte goes through one 16-entry table on the way
// out and each pair through one 256-entry table on the way in, whose
// invalid entries have bit 7 set, so a pair costs one test; the vector
// paths in hex_simd.mk take every whole block past 32 bytes.

import std.rt

const HexDigits: str = "0123456789abcdef"

// HexTable maps each byte to its value as a hex digit, and every other
// byte to 0xFF.
HexTable(): [256]u8 {
    var t: [256]u8 = [0xFF; 256]
    for i in 0..10 {
        t[0x30 + i] = Trunc[u8](i)
    }
    for i in 0..6 {
        t[0x41 + i] = Trunc[u8](10 + i)
        t[0x61 + i] = Trunc[u8](10 + i)
    }
    return t
}

const HexDec: [256]u8 = HexTable()

// HexEncodeTo writes the lower-case hex of data to dst, which holds
// 2 * data.Len() bytes.
HexEncodeTo(dst: mut []u8, data: []u8) {
    digits := HexDigits.AsBytes()
    var i := 0
    if data.Len() >= 32 {
        i = HexEncodeVec(mut dst[..], data)
    }
    for i < data.Len() {
        x := data[i]
        var d := mut dst[2 * i..2 * i + 2]
        d[0] = digits[int(x >> 4)]
        d[1] = digits[int(x & 15)]
        i += 1
    }
}

/// HexEncode is the lower-case hex of data, two characters a byte, in a.
/// O(n); allocates 2 * data.Len() bytes in a.
export HexEncode(a: &Arena, data: []u8): OutOfMemory!str from a {
    out := try a.Slice[u8](2 * data.Len())
    HexEncodeTo(mut out[..], data)
    // Must: every byte written is an ASCII hex digit.
    return rt.StrFrom(out[..]).Must()
}

/// HexDecode is the bytes s spells in hex, either case, in a. Fails with
/// BadInput at the first byte that is not a hex digit, else at the last
/// byte of an odd-length s. O(n); allocates s.Len() / 2 bytes in a, also
/// when the decode then fails.
export HexDecode(a: &Arena, s: str): Error|OutOfMemory![]u8 from a {
    b := s.AsBytes()
    n := b.Len()
    out := try a.Slice[u8](n / 2)
    var i := 0
    if n >= 64 {
        i = HexDecodeVec(mut out[..], b)
    }
    dec := &HexDec
    for i < n / 2 {
        hi := dec[int(b[2 * i])]
        lo := dec[int(b[2 * i + 1])]
        if (hi | lo) >= 0x80 {
            fail BadInput(offset: if hi >= 0x80 { 2 * i } else { 2 * i + 1 })
        }
        out[i] = (hi << 4) | lo
        i += 1
    }
    if n % 2 == 1 {
        fail BadInput(offset: n - 1)
    }
    return out[..]
}

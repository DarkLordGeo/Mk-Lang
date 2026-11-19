// Why: AES-GCM's authenticator needs carry-less multiplication, which a
// CPU without pclmul lacks, and a table-based GHASH leaks through the cache.
//
// What: GhashPortable, with GhashBmul and GhashRev64.
//
// How: GCM's GHASH (NIST SP 800-38D sections 6.3 and 6.4) on the portable
// path, by BearSSL's ghash_ctmul64 (Thomas Pornin, src/hash/ghash_ctmul64.c):
// carry-less 64 x 64 products from integer multiplications on operands with
// holes of three bits (bmul64), the high halves from the bit-reversed
// operands, Karatsuba over the two halves, then the reduction modulo x^128 +
// x^7 + x^2 + x + 1 in GCM's reflected bit order. Constant time on x86_64 and
// aarch64, whose 64-bit multiply takes the same time for every operand: no
// table and no branch on H or the data.
//
// Why no carry spoils a kept bit: in x0 * y0 (each with a bit at most
// every fourth place), the number of partial products at bit i is at most
// i / 4 + 1, below 16 for every i under 60, so their sum stays inside the
// three free bits above i; from bit 60 on, the overflow leaves the word.

// GhashBmul is the low 64 bits of the carry-less product of x and y.
GhashBmul(x: u64, y: u64): u64 {
    x0 := x & 0x1111111111111111
    x1 := x & 0x2222222222222222
    x2 := x & 0x4444444444444444
    x3 := x & 0x8888888888888888
    y0 := y & 0x1111111111111111
    y1 := y & 0x2222222222222222
    y2 := y & 0x4444444444444444
    y3 := y & 0x8888888888888888
    z0 := (x0 *% y0) ^ (x1 *% y3) ^ (x2 *% y2) ^ (x3 *% y1)
    z1 := (x0 *% y1) ^ (x1 *% y0) ^ (x2 *% y3) ^ (x3 *% y2)
    z2 := (x0 *% y2) ^ (x1 *% y1) ^ (x2 *% y0) ^ (x3 *% y3)
    z3 := (x0 *% y3) ^ (x1 *% y2) ^ (x2 *% y1) ^ (x3 *% y0)
    return (z0 & 0x1111111111111111) | (z1 & 0x2222222222222222) |
        (z2 & 0x4444444444444444) | (z3 & 0x8888888888888888)
}

// GhashRev64 is x with its bit order reversed.
GhashRev64(x: u64): u64 {
    var v := x
    v = ((v & 0x5555555555555555) << 1) | ((v >> 1) & 0x5555555555555555)
    v = ((v & 0x3333333333333333) << 2) | ((v >> 2) & 0x3333333333333333)
    v = ((v & 0x0F0F0F0F0F0F0F0F) << 4) | ((v >> 4) & 0x0F0F0F0F0F0F0F0F)
    v = ((v & 0x00FF00FF00FF00FF) << 8) | ((v >> 8) & 0x00FF00FF00FF00FF)
    v = ((v & 0x0000FFFF0000FFFF) << 16) | ((v >> 16) & 0x0000FFFF0000FFFF)
    return (v << 32) | (v >> 32)
}

// GhashPortable updates y over every whole 16-byte block X of data, in
// order, to (y ^ X) * h in GCM's GF(2^128); data.Len() % 16 trailing bytes
// are ignored. O(n): six 64-bit carry-less products per block.
GhashPortable(h: &[16]u8, y: mut [16]u8, data: []u8) {
    h1 := Be64(h[..], 0)
    h0 := Be64(h[..], 8)
    h0r := GhashRev64(h0)
    h1r := GhashRev64(h1)
    h2 := h0 ^ h1
    h2r := h0r ^ h1r
    var y1 := Be64(y[..], 0)
    var y0 := Be64(y[..], 8)
    n := data.Len() / 16 * 16
    var i := 0
    for i < n {
        y1 = y1 ^ Be64(data, i)
        y0 = y0 ^ Be64(data, i + 8)
        y0r := GhashRev64(y0)
        y1r := GhashRev64(y1)
        y2 := y0 ^ y1
        y2r := y0r ^ y1r
        z0 := GhashBmul(y0, h0)
        z1 := GhashBmul(y1, h1)
        var z2 := GhashBmul(y2, h2)
        var z0h := GhashBmul(y0r, h0r)
        var z1h := GhashBmul(y1r, h1r)
        var z2h := GhashBmul(y2r, h2r)
        z2 = z2 ^ z0 ^ z1
        z2h = z2h ^ z0h ^ z1h
        z0h = GhashRev64(z0h) >> 1
        z1h = GhashRev64(z1h) >> 1
        z2h = GhashRev64(z2h) >> 1
        var v0 := z0
        var v1 := z0h ^ z2
        var v2 := z1 ^ z2h
        var v3 := z1h
        v3 = (v3 << 1) | (v2 >> 63)
        v2 = (v2 << 1) | (v1 >> 63)
        v1 = (v1 << 1) | (v0 >> 63)
        v0 = v0 << 1
        v2 = v2 ^ v0 ^ (v0 >> 1) ^ (v0 >> 2) ^ (v0 >> 7)
        v1 = v1 ^ (v0 << 63) ^ (v0 << 62) ^ (v0 << 57)
        v3 = v3 ^ v1 ^ (v1 >> 1) ^ (v1 >> 2) ^ (v1 >> 7)
        v2 = v2 ^ (v1 << 63) ^ (v1 << 62) ^ (v1 << 57)
        y0 = v2
        y1 = v3
        i += 16
    }
    PutBe64(mut y[..], 0, y1)
    PutBe64(mut y[..], 8, y0)
}

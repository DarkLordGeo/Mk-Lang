// Why: Sin, Cos and Tan of a huge argument need x - n * pi/2 to the last
// bit, which Cody and Waite's subtraction cannot give past 2^20 * pi/2.
//
// What: RemPio2Large (the octant and the remainder as a double-double)
// and FourOverPi, the bits of 4/pi it reads.
//
// How: Payne and Hanek's method ("Radian reduction for trigonometric
// functions", 1983; K. C. Ng, "Argument reduction for huge arguments: good
// to the last bit", 1992) on 64-bit words: x's 53-bit mantissa times the
// 192 bits of 4/pi that matter at x's exponent gives the octant and 125
// bits of fraction, enough for the worst case of the f64 range.
// TestSTDMATH_Reduction runs the f64 nearest a multiple of pi/2 in every
// binade from 2^20 to 2^1023. O(1); allocates nothing.

import std.raw

// FourOverPi holds the bits of 4/pi, FourOverPi[i] the i-th 64-bit digit:
// 4/pi = sum of FourOverPi[i] * 2^(-64 i); 1217 bits cover x's largest
// exponent. Checked against mpmath at 1400 bits by a Go test.
const FourOverPi: [20]u64 = [
    0x0000000000000001, 0x45F306DC9C882A53, 0xF84EAFA3EA69BB81, 0xB6C52B3278872083,
    0xFCA2C757BD778AC3, 0x6E48DC74849BA5C0, 0x0C925DD413A32439, 0xFC3BD63962534E7D,
    0xD1046BEA5D768909, 0xD338E04D68BEFC82, 0x7323AC7306A673E9, 0x3908BF177BF25076,
    0x3FF12FFFBC0B301F, 0xDE5E2316B414DA3E, 0xDA6CFD9E4F96136E, 0x9E8C7ECD3CBFD45A,
    0xEA4F758FD7CBE2F6, 0x7A0E73EF14A525D4, 0xD7F6BF623F1ABA10, 0xAC06608DF8F6D757,
]

// pi/2 as a double-double.
const Pio2Hi: f64 = f64.FromBits(0x3FF921FB54442D18)
const Pio2Lo: f64 = f64.FromBits(0x3C91A62633145C07)

// Digit is the 64 bits of 4/pi starting shift bits into FourOverPi[d].
Digit(d: int, shift: int): u64 {
    if shift == 0 {
        return FourOverPi[d]
    }
    return FourOverPi[d] << shift | FourOverPi[d + 1] >> (64 - shift)
}

// RemPio2Large reduces a finite |x| >= 2^20 * pi/2.
RemPio2Large(x: f64): Reduced {
    b := x.Bits()
    neg := b & SignBit64 != 0
    m := b & FracMask64 | 0x0010000000000000
    // x = m * 2^e, e >= -32 here, and the product's digit at 2^-61 aligns
    // with the third bit of the top word, so its top 3 bits are the
    // octant mod 8.
    e := Trunc[int]((b >> 52) & 0x7FF) - 1075
    d := (e + 61) / 64
    shift := (e + 61) % 64
    z0 := Digit(d, shift)
    z1 := Digit(d + 1, shift)
    z2 := Digit(d + 2, shift)
    p2 := raw.Mul128(z2, m)
    p1 := raw.Mul128(z1, m)
    lo := p1.lo +% p2.hi
    carry: u64 = if lo < p1.lo { 1 } else { 0 }
    hi := (z0 *% m) +% p1.hi +% carry
    // x * 2/pi = hi:lo / 2: the top 2 bits are n mod 4 and the other 126
    // bits the fraction g; a fraction of a half or more counts as g - 1,
    // n + 1, so |g| <= 1/2.
    var gh := hi << 2 | lo >> 62
    var gl := lo << 2
    // Without a branch (g's top bit is a coin flip): when g >= 1/2, top is
    // 1 and gm all ones, so n + 1 and 1 - g as the two's complement of the
    // 128-bit g (~gl + 1, and ~gh plus the carry out of it, which is 1
    // exactly when gl = 0); else top and gm are 0 and both stay.
    top := gh >> 63
    n := Trunc[int](hi >> 62) + Trunc[int](top)
    gm := 0 -% top
    inc: u64 = if gl == 0 { 1 } else { 0 }
    gl = (gl ^ gm) -% gm
    gh = (gh ^ gm) +% (inc & gm)
    gneg := top != 0
    // g = gh:gl * 2^-128; normalize and split into two f64s.
    lz := if gh == 0 { 64 + raw.Clz64(gl) } else { raw.Clz64(gh) }
    if lz >= 128 {
        return Reduced(quadrant: if neg { (4 - (n & 3)) & 3 } else { n & 3 }, y0: 0.0, y1: 0.0)
    }
    var th := gh
    var tl := gl
    if lz >= 64 {
        th = gl << (lz - 64)
        tl = 0
    } else if lz > 0 {
        th = gh << lz | gl >> (64 - lz)
        tl = gl << lz
    }
    // th:tl = g * 2^(128 + lz); its top 53 bits exactly, then the next 64
    // rounded once (by the conversion), each scaled by a product with a
    // power of two, exact as LdExp is: lz < 128, so 2^(-53 - lz) and
    // 2^(-117 - lz) are normal, and so is a nonzero product (at least
    // 2^-244).
    fh := Cast[f64](th >> 11) * Pow2(-53 - lz)
    rest := (th & 0x7FF) << 53 | tl >> 11
    fl := Cast[f64](rest) * Pow2(-117 - lz)
    // y = (fh + fl) * pi/2 as y0 + y1: Dekker's product for fh * Pio2Hi.
    c := 134217729.0 * fh
    hh := c - (c - fh)
    hl := fh - hh
    p := fh * Pio2Hi
    err := ((hh * Pio2SplitHi - p) + hh * Pio2SplitLo + hl * Pio2SplitHi) + hl * Pio2SplitLo
    t := err + (fh * Pio2Lo + fl * Pio2Hi)
    y0 := p + t
    y1 := t - (y0 - p)
    // The sign: g's, then x's (sin and tan are odd, cos even, so -x gives
    // -n and -y), flipped on the sign bits, which is negation exactly.
    sm: u64 = if gneg != neg { SignBit64 } else { 0 }
    q := if neg { (4 - (n & 3)) & 3 } else { n & 3 }
    return Reduced(quadrant: q, y0: f64.FromBits(y0.Bits() ^ sm), y1: f64.FromBits(y1.Bits() ^ sm))
}

// Pio2Hi split into 27 and 26 bits for Dekker's product.
const Pio2SplitHi: f64 = f64.FromBits(0x3FF921FB54000000)
const Pio2SplitLo: f64 = Pio2Hi - Pio2SplitHi

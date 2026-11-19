// Why: Sin, Cos and Tan must give the same bits on every target and in the
// evaluator, over the whole f64 range.
//
// What: Sin, Cos and Tan, the kernels KSin, KCos and KTan, and the
// reduction RemPio2.
//
// How: FreeBSD msun's s_sin.c, s_cos.c, s_tan.c over the kernels k_sin.c,
// k_cos.c, k_tan.c, with e_rem_pio2.c's reduction: Cody and Waite's
// three-step subtraction of n * pi/2 below 2^20 * pi/2, and Payne and
// Hanek's reduction above (reduce.mk). All O(1), no allocation, no panic;
// an infinity or a NaN gives Nan.

// k_sin.c's polynomial for sin on [-pi/4, pi/4].
const S1: f64 = f64.FromBits(0xBFC5555555555549) // -1.66666666666666324348e-01
const S2: f64 = f64.FromBits(0x3F8111111110F8A6) // 8.33333333332248946124e-03
const S3: f64 = f64.FromBits(0xBF2A01A019C161D5) // -1.98412698298579493134e-04
const S4: f64 = f64.FromBits(0x3EC71DE357B1FE7D) // 2.75573137070700676789e-06
const S5: f64 = f64.FromBits(0xBE5AE5E68A2B9CEB) // -2.50507602534068634195e-08
const S6: f64 = f64.FromBits(0x3DE5D93A5ACFD57C) // 1.58969099521155010221e-10

// k_cos.c's polynomial for cos on [-pi/4, pi/4].
const C1: f64 = f64.FromBits(0x3FA555555555554C) // 4.16666666666666019037e-02
const C2: f64 = f64.FromBits(0xBF56C16C16C15177) // -1.38888888888741095749e-03
const C3: f64 = f64.FromBits(0x3EFA01A019CB1590) // 2.48015872894767294178e-05
const C4: f64 = f64.FromBits(0xBE927E4F809C52AD) // -2.75573143513906633035e-07
const C5: f64 = f64.FromBits(0x3E21EE9EBDB4B1C4) // 2.08757232129817482790e-09
const C6: f64 = f64.FromBits(0xBDA8FAE9BE8838D4) // -1.13596475577881948265e-11

// k_tan.c's polynomial for tan on [-0.67434, 0.67434].
const T0: f64 = f64.FromBits(0x3FD5555555555563)        // 3.33333333333334091986e-01
const T1: f64 = f64.FromBits(0x3FC111111110FE7A)        // 1.33333333333201242699e-01
const T2: f64 = f64.FromBits(0x3FABA1BA1BB341FE)        // 5.39682539762260521377e-02
const T3: f64 = f64.FromBits(0x3F9664F48406D637)        // 2.18694882948595424599e-02
const T4: f64 = f64.FromBits(0x3F8226E3E96E8493)        // 8.86323982359930005737e-03
const T5: f64 = f64.FromBits(0x3F6D6D22C9560328)        // 3.59207910759131235356e-03
const T6: f64 = f64.FromBits(0x3F57DBC8FEE08315)        // 1.45620945432529025516e-03
const T7: f64 = f64.FromBits(0x3F4344D8F2F26501)        // 5.88041240820264096874e-04
const T8: f64 = f64.FromBits(0x3F3026F71A8D1068)        // 2.46463134818469906812e-04
const T9: f64 = f64.FromBits(0x3F147E88A03792A6)        // 7.81794442939557092300e-05
const T10: f64 = f64.FromBits(0x3F12B80F32F0A7E9)       // 7.14072491382608190305e-05
const T11: f64 = f64.FromBits(0xBEF375CBDB605373)       // -1.85586374855275456654e-05
const T12: f64 = f64.FromBits(0x3EFB2A7074BF7AD4)       // 2.59073051863633712884e-05
const PiOver4: f64 = f64.FromBits(0x3FE921FB54442D18)   // 7.85398163397448278999e-01
const PiOver4Lo: f64 = f64.FromBits(0x3C81A62633145C07) // 3.06161699786838301793e-17

// e_rem_pio2.c's constants: 2/pi, and pi/2 in three 33-bit pieces with the
// tail after each.
const InvPio2: f64 = f64.FromBits(0x3FE45F306DC9C883) // 6.36619772367581382433e-01
const Pio2P1: f64 = f64.FromBits(0x3FF921FB54400000)  // 1.57079632673412561417e+00
const Pio2P1T: f64 = f64.FromBits(0x3DD0B4611A626331) // 6.07710050650619224932e-11
const Pio2P2: f64 = f64.FromBits(0x3DD0B4611A600000)  // 6.07710050630396597660e-11
const Pio2P2T: f64 = f64.FromBits(0x3BA3198A2E037073) // 2.02226624879595063154e-21
const Pio2P3: f64 = f64.FromBits(0x3BA3198A2E000000)  // 2.02226624871116645580e-21
const Pio2P3T: f64 = f64.FromBits(0x397B839A252049C1) // 8.47842766036889956997e-32
// 1.5 * 2^52: adding and subtracting it rounds an |x| < 2^51 to an integer.
const ToInt: f64 = f64.FromBits(0x4338000000000000)

// Reduced is x - n * pi/2 as y0 + y1, |y0 + y1| <= pi/4 (a little more on
// the Cody-Waite path), with n mod 4 in quadrant.
struct Reduced {
    quadrant: int
    y0: f64
    y1: f64
}

// RemPio2 reduces a finite x with |x| > pi/4 (e_rem_pio2.c).
RemPio2(x: f64): Reduced {
    a := x.Bits() & AbsMask64
    if a >= 0x413921FB00000000 {
        return RemPio2Large(x)
    }
    // Medium: n = rint(x * 2/pi), then subtract n * pi/2 in up to three
    // pieces, stopping once the result has kept enough bits.
    fnr := (x * InvPio2 + ToInt) - ToInt
    n := Cast[int](fnr)
    var r := x - fnr * Pio2P1
    var w := fnr * Pio2P1T
    j := Trunc[int](a >> 52)
    var y0 := r - w
    var i := j - Trunc[int]((y0.Bits() >> 52) & 0x7FF)
    if i > 16 {
        // A second iteration, good to 118 bits.
        var t := r
        w = fnr * Pio2P2
        r = t - w
        w = fnr * Pio2P2T - ((t - r) - w)
        y0 = r - w
        i = j - Trunc[int]((y0.Bits() >> 52) & 0x7FF)
        if i > 49 {
            // A third, good to 151 bits: covers every case.
            t = r
            w = fnr * Pio2P3
            r = t - w
            w = fnr * Pio2P3T - ((t - r) - w)
            y0 = r - w
        }
    }
    y1 := (r - y0) - w
    return Reduced(quadrant: n & 3, y0: y0, y1: y1)
}

// KSin is sin(x + y) for |x + y| <= pi/4, y the tail of x; iy 0 when y is
// known to be 0 (k_sin.c).
KSin(x: f64, y: f64, iy: int): f64 {
    z := x * x
    w := z * z
    r := S2 + z * (S3 + z * S4) + z * w * (S5 + z * S6)
    v := z * x
    if iy == 0 {
        return x + v * (S1 + z * r)
    }
    return x - ((z * (0.5 * y - v * r) - y) - v * S1)
}

// KCos is cos(x + y) for |x + y| <= pi/4 (k_cos.c).
KCos(x: f64, y: f64): f64 {
    z := x * x
    w := z * z
    r := z * (C1 + z * (C2 + z * C3)) + w * w * (C4 + z * (C5 + z * C6))
    hz := 0.5 * z
    v := 1.0 - hz
    return v + (((1.0 - v) - hz) + (z * r - x * y))
}

// KTan is tan(x + y) for |x + y| <= pi/4 when iy is 1, -1 / tan(x + y)
// when iy is -1 (k_tan.c); near pi/4 it uses tan(pi/4 - x).
KTan(x0: f64, y0: f64, iy: int): f64 {
    var x := x0
    var y := y0
    neg := x.Bits() & SignBit64 != 0
    big := x.Bits() & AbsMask64 >= 0x3FE5942800000000
    if big {
        if neg {
            x = -x
            y = -y
        }
        x = (PiOver4 - x) + (PiOver4Lo - y)
        y = 0.0
    }
    z := x * x
    w := z * z
    // The odd and even halves of the polynomial, summed as in k_tan.c.
    var r := T1 + w * (T3 + w * (T5 + w * (T7 + w * (T9 + w * T11))))
    v := z * (T2 + w * (T4 + w * (T6 + w * (T8 + w * (T10 + w * T12)))))
    s := z * x
    r = y + z * (s * (r + v) + y)
    r += T0 * s
    sum := x + r
    if big {
        fiy := Cast[f64](iy)
        t := fiy - 2.0 * (x - (sum * sum / (sum + fiy) - r))
        return if neg { -t } else { t }
    }
    if iy == 1 {
        return sum
    }
    // -1 / (x + r), with the division's error compensated: a and t are
    // -1 / sum, t and z cut to their high words.
    zh := f64.FromBits(sum.Bits() & 0xFFFFFFFF00000000)
    v2 := r - (zh - x)
    a := -1.0 / sum
    t := f64.FromBits(a.Bits() & 0xFFFFFFFF00000000)
    s2 := 1.0 + t * zh
    return t + a * (s2 + t * v2)
}

/// Sin is the sine of x in radians: Nan for an infinity or a NaN. Faithfully
/// rounded (within 1 ulp) but not correctly rounded, so its results may change
/// in a minor release. TestSTDMATH_TrueError: largest error 0.715 ulp over
/// 20,271 inputs, 501 of them not correctly rounded. Over the 2,010 inputs
/// nearest a multiple of pi/2 of TestSTDMATH_Reduction, largest error 0.500
/// ulp. O(1), a large x included; allocates nothing.
export Sin(x: f64): f64 {
    a := x.Bits() & AbsMask64
    if a <= 0x3FE921FBFFFFFFFF {
        if a < 0x3E50000000000000 {
            return x // |x| < 2^-26: sin x rounds to x
        }
        return KSin(x, 0.0, 0)
    }
    if a >= ExpMask64 {
        return Nan
    }
    r := RemPio2(x)
    return match r.quadrant {
        0 { KSin(r.y0, r.y1, 1) }
        1 { KCos(r.y0, r.y1) }
        2 { -KSin(r.y0, r.y1, 1) }
        _ { -KCos(r.y0, r.y1) }
    }
}

/// Cos is the cosine of x in radians: Nan for an infinity or a NaN. Faithfully
/// rounded (within 1 ulp) but not correctly rounded, so its results may change
/// in a minor release. TestSTDMATH_TrueError: largest error 0.725 ulp over
/// 20,271 inputs, 485 of them not correctly rounded. Over the 2,010 inputs
/// nearest a multiple of pi/2 of TestSTDMATH_Reduction, largest error 0.500
/// ulp. O(1); allocates nothing.
export Cos(x: f64): f64 {
    a := x.Bits() & AbsMask64
    if a <= 0x3FE921FBFFFFFFFF {
        if a < 0x3E46A09E00000000 {
            return 1.0 // |x| < 2^-27 * sqrt(2): cos x rounds to 1
        }
        return KCos(x, 0.0)
    }
    if a >= ExpMask64 {
        return Nan
    }
    r := RemPio2(x)
    return match r.quadrant {
        0 { KCos(r.y0, r.y1) }
        1 { -KSin(r.y0, r.y1, 1) }
        2 { -KCos(r.y0, r.y1) }
        _ { KSin(r.y0, r.y1, 1) }
    }
}

/// Tan is the tangent of x in radians: Nan for an infinity or a NaN. Faithfully
/// rounded (within 1 ulp) but not correctly rounded, so its results may change
/// in a minor release. TestSTDMATH_TrueError: largest error 0.778 ulp over
/// 20,257 inputs, 580 of them not correctly rounded. Over the 2,010 inputs
/// nearest a multiple of pi/2 of TestSTDMATH_Reduction, largest error 0.500
/// ulp. O(1); allocates nothing.
export Tan(x: f64): f64 {
    a := x.Bits() & AbsMask64
    if a <= 0x3FE921FBFFFFFFFF {
        if a < 0x3E40000000000000 {
            return x // |x| < 2^-27
        }
        return KTan(x, 0.0, 1)
    }
    if a >= ExpMask64 {
        return Nan
    }
    r := RemPio2(x)
    return KTan(r.y0, r.y1, 1 - (r.quadrant & 1) * 2)
}

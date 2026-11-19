// Why: Exp2, Expm1, Log2, Log10 and Log1p must give the same bits on every
// target and in the evaluator, so they are plain Mk over IEEE operations.
//
// What: Exp2, Expm1, Log2, Log10 and Log1p, the shared ExpKernel, and
// LogNear1, the msun logarithm Log uses near 1.
//
// How: FreeBSD msun's e_exp.c (ExpKernel), s_expm1.c, e_log.c, e_log2.c,
// e_log10.c (with k_log.h) and s_log1p.c over the u64 encoding. Exp2
// reduces x to k + r exactly and takes r * ln2 as a double-double by
// Dekker's product, then shares ExpKernel. Exp and Log themselves are
// table-driven (exptab.mk, logtab.mk). Constants are written as their bits,
// each beside its value in the msun source. All O(1), no allocation, no
// panic; a NaN result is Nan.

// Exp's reduction: ln2 split so that k * Ln2Hi is exact for |k| < 2^11.
const Ln2Hi: f64 = f64.FromBits(0x3FE62E42FEE00000)  // 6.93147180369123816490e-01
const Ln2Lo: f64 = f64.FromBits(0x3DEA39EF35793C76)  // 1.90821492927058770002e-10
const InvLn2: f64 = f64.FromBits(0x3FF71547652B82FE) // 1.44269504088896338700e+00
// The f64 nearest to ln2, and ln2 less it.
const Ln2Near: f64 = f64.FromBits(0x3FE62E42FEFA39EF)
const Ln2Tail: f64 = f64.FromBits(0x3C7ABC9E3B39803F)

// Exp's polynomial on [-0.5 ln2, 0.5 ln2] (e_exp.c P1..P5).
const ExpP1: f64 = f64.FromBits(0x3FC555555555553E) // 1.66666666666666019037e-01
const ExpP2: f64 = f64.FromBits(0xBF66C16C16BEBD93) // -2.77777777770155933842e-03
const ExpP3: f64 = f64.FromBits(0x3F11566AAF25DE2C) // 6.61375632143793436117e-05
const ExpP4: f64 = f64.FromBits(0xBEBBBD41C5D26BF1) // -1.65339022054652515390e-06
const ExpP5: f64 = f64.FromBits(0x3E66376972BEA4D0) // 4.13813679705723846039e-08

// Expm1's threshold: above ExpOver the result overflows.
const ExpOver: f64 = f64.FromBits(0x40862E42FEFA39EF) // 7.09782712893383973096e+02

// The log kernel's polynomial (e_log.c Lg1..Lg7).
const Lg1: f64 = f64.FromBits(0x3FE5555555555593) // 6.666666666666735130e-01
const Lg2: f64 = f64.FromBits(0x3FD999999997FA04) // 3.999999999940941908e-01
const Lg3: f64 = f64.FromBits(0x3FD2492494229359) // 2.857142874366239149e-01
const Lg4: f64 = f64.FromBits(0x3FCC71C51D8E78AF) // 2.222219843214978396e-01
const Lg5: f64 = f64.FromBits(0x3FC7466496CB03DE) // 1.818357216161805012e-01
const Lg6: f64 = f64.FromBits(0x3FC39A09D078C69F) // 1.531383769920937332e-01
const Lg7: f64 = f64.FromBits(0x3FC2F112DF3E5244) // 1.479819860511658591e-01

// Log2's and Log10's split multipliers (e_log2.c, e_log10.c).
const InvLn2Hi: f64 = f64.FromBits(0x3FF7154765200000)   // 1.44269504072144627571e+00
const InvLn2Lo: f64 = f64.FromBits(0x3DE705FC2EEFA200)   // 1.67517131648865118353e-10
const InvLn10Hi: f64 = f64.FromBits(0x3FDBCB7B15200000)  // 4.34294481878168880939e-01
const InvLn10Lo: f64 = f64.FromBits(0x3DBB9438CA9AADD5)  // 2.50829467116452752298e-11
const Log10Of2Hi: f64 = f64.FromBits(0x3FD34413509F6000) // 3.01029995663611771306e-01
const Log10Of2Lo: f64 = f64.FromBits(0x3D59FEF311F12B36) // 3.69423907715893078616e-13

// Expm1's polynomial (s_expm1.c Q1..Q5).
const Q1: f64 = f64.FromBits(0xBFA11111111110F4) // -3.33333333333331316428e-02
const Q2: f64 = f64.FromBits(0x3F5A01A019FE5585) // 1.58730158725481460165e-03
const Q3: f64 = f64.FromBits(0xBF14CE199EAADBB7) // -7.93650757867487942473e-05
const Q4: f64 = f64.FromBits(0x3ED0CFCA86E65239) // 4.00821782732936239552e-06
const Q5: f64 = f64.FromBits(0xBE8AFDB76E09C32D) // -2.01099218183624371326e-07

const Two54: f64 = f64.FromBits(0x4350000000000000)

// ExpKernel is e^(hi - lo) * 2^k for |hi - lo| <= 0.5 ln2 (e_exp.c's
// final step), scaled by LdExp, which rounds once.
ExpKernel(hi: f64, lo: f64, k: int): f64 {
    r := hi - lo
    t := r * r
    c := r - t * (ExpP1 + t * (ExpP2 + t * (ExpP3 + t * (ExpP4 + t * ExpP5))))
    y := 1.0 - ((lo - (r * c) / (2.0 - c)) - hi)
    return LdExp(y, k)
}

/// Exp2 is 2^x: exact at an integer x, inf from 1024 up, 0 at -1075 and below,
/// Nan for a NaN. Faithfully rounded (within 1 ulp) but not correctly rounded,
/// so its results may change in a minor release. TestSTDMATH_TrueError:
/// largest error 0.803 ulp over 20,283 inputs, 933 of them not correctly
/// rounded. O(1); allocates nothing.
export Exp2(x: f64): f64 {
    b := x.Bits()
    a := b & AbsMask64
    if a >= ExpMask64 {
        return if a > ExpMask64 { Nan } else if b == ExpMask64 { x } else { 0.0 }
    }
    if x >= 1024.0 {
        return Inf
    }
    if x <= -1075.0 {
        return 0.0
    }
    kf := Round(x)
    k := Cast[int](kf)
    r := x - kf // exact, |r| <= 0.5
    if r == 0.0 {
        return LdExp(1.0, k)
    }
    // r * ln2 as p + e by Dekker's product with Veltkamp's split.
    c := 134217729.0 * r
    rh := c - (c - r)
    rl := r - rh
    p := r * Ln2Near
    e := ((rh * Ln2NearHi - p) + rh * Ln2NearLo + rl * Ln2NearHi) + rl * Ln2NearLo + r * Ln2Tail
    return ExpKernel(p, -e, k)
}

// Ln2Near split into 27 and at most 26 bits, so each product with a split
// half of r (26 bits each) is exact.
const Ln2NearHi: f64 = f64.FromBits(0x3FE62E42FC000000)
const Ln2NearLo: f64 = Ln2Near - Ln2NearHi

/// Expm1 is e^x - 1, accurate near 0, where Exp(x) - 1 cancels: -1 below -56
/// ln2, inf past 709.78, Nan for a NaN. Faithfully rounded (within 1 ulp) but
/// not correctly rounded, so its results may change in a minor release.
/// TestSTDMATH_TrueError: largest error 0.743 ulp over 20,221 inputs, 922 of
/// them not correctly rounded. O(1); allocates nothing.
export Expm1(x: f64): f64 {
    b := x.Bits()
    a := b & AbsMask64
    if a >= ExpMask64 {
        return if a > ExpMask64 { Nan } else if b == ExpMask64 { x } else { -1.0 }
    }
    neg := b & SignBit64 != 0
    if a >= 0x4043687A9F1AF2B1 {
        // |x| >= 56 ln2
        if neg {
            return -1.0
        }
        if x > ExpOver {
            return Inf
        }
    }
    var v := x
    var c := 0.0
    var k := 0
    if a > 0x3FD62E42FEFA39EF {
        // |x| > 0.5 ln2
        var hi := 0.0
        var lo := 0.0
        if a < 0x3FF0A2B23F3BAB73 {
            if !neg {
                hi = x - Ln2Hi
                lo = Ln2Lo
                k = 1
            } else {
                hi = x + Ln2Hi
                lo = -Ln2Lo
                k = -1
            }
        } else {
            half := CopySign(0.5, x)
            k = Cast[int](InvLn2 * x + half)
            t := Cast[f64](k)
            hi = x - t * Ln2Hi
            lo = t * Ln2Lo
        }
        v = hi - lo
        c = (hi - v) - lo
    } else if a < 0x3C90000000000000 {
        return x // |x| < 2^-54
    }
    hfx := 0.5 * v
    hxs := v * hfx
    r1 := 1.0 + hxs * (Q1 + hxs * (Q2 + hxs * (Q3 + hxs * (Q4 + hxs * Q5))))
    t := 3.0 - r1 * hfx
    var e := hxs * ((r1 - t) / (6.0 - v * t))
    if k == 0 {
        return v - (v * e - hxs)
    }
    e = v * (e - c) - c
    e -= hxs
    if k == -1 {
        return 0.5 * (v - e) - 0.5
    }
    if k == 1 {
        if v < -0.25 {
            return -2.0 * (e - (v + 0.5))
        }
        return 1.0 + 2.0 * (v - e)
    }
    if k <= -2 || k > 56 {
        y := 1.0 - (e - v)
        return LdExp(y, k) - 1.0
    }
    if k < 20 {
        t1 := f64.FromBits(0x3FF0000000000000 - (0x20000000000000 >> k)) // 1 - 2^-k
        return LdExp(t1 - (e - v), k)
    }
    t2 := f64.FromBits(Trunc[u64](0x3FF - k) << 52) // 2^-k
    return LdExp((v - (e + t2)) + 1.0, k)
}

// LogReduce writes a positive finite x as 2^k * m with m in
// [sqrt(2)/2, sqrt(2)), returning m's bits and k (e_log.c's reduction:
// the 0x95F64 offset moves the cut from 2 to sqrt(2)).
LogReduce(x: f64): (m: f64, k: int) {
    var b := x.Bits()
    var k := 0
    if b < 0x0010000000000000 {
        b = (x * Two54).Bits() // subnormal: scale into the normal range
        k = -54
    }
    hx := Trunc[int](b >> 32)
    k += (hx >> 20) - 1023
    f := hx & 0x000FFFFF
    i := (f + 0x95F64) & 0x100000
    k += i >> 20
    m := f64.FromBits(Trunc[u64](f | (i ^ 0x3FF00000)) << 32 | b & 0xFFFFFFFF)
    return (m: m, k: k)
}

// LogSpecial is the result for an x Log, Log2 and Log10 do not reduce: a
// NaN or a value below zero is Nan, a zero -inf, +inf itself; none for a
// positive finite x.
LogSpecial(x: f64): ?f64 {
    b := x.Bits()
    if b == 0 || b == SignBit64 {
        return some(-Inf)
    }
    if b >= ExpMask64 {
        return some(if b == ExpMask64 { x } else { Nan })
    }
    return none
}

// LogNear1 is ln x for x in [1 - 2^-4, 1 + 0x1.09p-4], where Log's table
// path would lose relative accuracy as ln x nears 0: e_log.c with k = 0
// (x is its own reduced argument there).
LogNear1(x: f64): f64 {
    f := x - 1.0
    hx := Trunc[int](x.Bits() >> 32) & 0x000FFFFF
    if (0x000FFFFF & (2 + hx)) < 3 {
        // -2^-20 <= f < 2^-20
        if f == 0.0 {
            return 0.0
        }
        return f - f * f * (0.5 - 0.33333333333333333 * f)
    }
    s := f / (2.0 + f)
    z := s * s
    w := z * z
    t1 := w * (Lg2 + w * (Lg4 + w * Lg6))
    t2 := z * (Lg1 + w * (Lg3 + w * (Lg5 + w * Lg7)))
    rr := t2 + t1
    // Whether f lies outside [0.0742, 0.1069] (hx between 0x6147A and 0x6B851).
    if (hx - 0x6147A) | (0x6B851 - hx) > 0 {
        hfsq := 0.5 * f * f
        return f - (hfsq - s * (hfsq + rr))
    }
    return f - s * (f - rr)
}

// KLog1p is log(1 + f) - f + f * f / 2 for f in [sqrt(2)/2 - 1,
// sqrt(2) - 1] (k_log.h), the part Log2 and Log10 add to their split of f.
KLog1p(f: f64): f64 {
    s := f / (2.0 + f)
    z := s * s
    w := z * z
    t1 := w * (Lg2 + w * (Lg4 + w * Lg6))
    t2 := z * (Lg1 + w * (Lg3 + w * (Lg5 + w * Lg7)))
    hfsq := 0.5 * f * f
    return s * (hfsq + t2 + t1)
}

/// Log2 is the base-2 logarithm: exact at a power of two, -inf at a zero, Nan
/// below zero or for a NaN. Faithfully rounded (within 1 ulp) but not correctly
/// rounded, so its results may change in a minor release.
/// TestSTDMATH_TrueError: largest error 0.518 ulp over 20,181 inputs, 7 of
/// them not correctly rounded. O(1); allocates nothing.
export Log2(x: f64): f64 {
    if s := LogSpecial(x) {
        return s
    }
    r := LogReduce(x)
    f := r.m - 1.0
    y := Cast[f64](r.k)
    if f == 0.0 {
        return y
    }
    hfsq := 0.5 * f * f
    rr := KLog1p(f)
    // f - hfsq as hi + lo, hi with its low word cleared so hi * InvLn2Hi
    // is exact.
    hi := f64.FromBits((f - hfsq).Bits() & 0xFFFFFFFF00000000)
    lo := (f - hi) - hfsq + rr
    var val_hi := hi * InvLn2Hi
    var val_lo := (lo + hi) * InvLn2Lo + lo * InvLn2Hi
    w := y + val_hi
    val_lo += (y - w) + val_hi
    val_hi = w
    return val_lo + val_hi
}

/// Log10 is the base-10 logarithm: -inf at a zero, Nan below zero or for a NaN.
/// Faithfully rounded (within 1 ulp) but not correctly rounded, so its results
/// may change in a minor release. TestSTDMATH_TrueError: largest error 0.576
/// ulp over 20,203 inputs, 14 of them not correctly rounded. O(1); allocates
/// nothing.
export Log10(x: f64): f64 {
    if s := LogSpecial(x) {
        return s
    }
    r := LogReduce(x)
    f := r.m - 1.0
    y := Cast[f64](r.k)
    if f == 0.0 && r.k == 0 {
        return 0.0
    }
    hfsq := 0.5 * f * f
    rr := KLog1p(f)
    hi := f64.FromBits((f - hfsq).Bits() & 0xFFFFFFFF00000000)
    lo := (f - hi) - hfsq + rr
    val_hi := hi * InvLn10Hi
    y2 := y * Log10Of2Hi
    var val_lo := y * Log10Of2Lo + (lo + hi) * InvLn10Lo + lo * InvLn10Hi
    w := y2 + val_hi
    val_lo += (y2 - w) + val_hi
    return val_lo + w
}

/// Log1p is log(1 + x), accurate near 0, where Log(1 + x) loses x's low bits:
/// -inf at -1, Nan below -1 or for a NaN. Faithfully rounded (within 1 ulp) but
/// not correctly rounded, so its results may change in a minor release.
/// TestSTDMATH_TrueError: largest error 0.727 ulp over 20,181 inputs, 630 of
/// them not correctly rounded. O(1); allocates nothing.
export Log1p(x: f64): f64 {
    b := x.Bits()
    a := b & AbsMask64
    if a > ExpMask64 || x < -1.0 {
        return Nan
    }
    if x == -1.0 {
        return -Inf
    }
    if b == ExpMask64 {
        return x
    }
    var f := 0.0
    var iu: u64 = 0
    var k := 1
    if a < 0x3FDA827999FCEF34 {
        // |x| < sqrt(2) - 1
        if a < 0x3E20000000000000 {
            // |x| < 2^-29
            if a < 0x3C90000000000000 {
                return x
            }
            return x - x * x * 0.5
        }
        if b < 0xBFD2BEC333018866 || b & SignBit64 == 0 {
            // sqrt(2)/2 - 1 < x
            k = 0
            f = x
            iu = 1
        }
    }
    var c := 0.0
    if k != 0 {
        var u := 0.0
        if a < 0x4340000000000000 {
            // |x| < 2^53
            u = 1.0 + x
            iu = u.Bits()
            k = Trunc[int](iu >> 52) - 1023
            c = if k > 0 { 1.0 - (u - x) } else { x - (u - 1.0) }
            c = c / u
        } else {
            u = x
            iu = u.Bits()
            k = Trunc[int](iu >> 52) - 1023
        }
        iu = iu & FracMask64
        if iu < 0x0006A09E667F3BCD {
            u = f64.FromBits(iu | 0x3FF0000000000000)
        } else {
            k += 1
            u = f64.FromBits(iu | 0x3FE0000000000000)
            iu = (0x0010000000000000 - iu) >> 2
        }
        f = u - 1.0
    }
    hfsq := 0.5 * f * f
    dk := Cast[f64](k)
    if iu == 0 {
        // |f| < 2^-20
        if f == 0.0 {
            if k == 0 {
                return 0.0
            }
            return dk * Ln2Hi + (c + dk * Ln2Lo)
        }
        rr := hfsq * (1.0 - 0.66666666666666666 * f)
        if k == 0 {
            return f - rr
        }
        return dk * Ln2Hi - ((rr - (dk * Ln2Lo + c)) - f)
    }
    s := f / (2.0 + f)
    z := s * s
    rr := z * (Lg1 + z * (Lg2 + z * (Lg3 + z * (Lg4 + z * (Lg5 + z * (Lg6 + z * Lg7))))))
    if k == 0 {
        return f - (hfsq - s * (hfsq + rr))
    }
    return dk * Ln2Hi - ((hfsq - (s * (hfsq + rr) + (dk * Ln2Lo + c))) - f)
}

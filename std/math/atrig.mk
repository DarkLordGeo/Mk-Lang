// Why: Asin, Acos, Atan and Atan2 must return the same bits on every target
// and in the evaluator, so they are written in plain Mk over IEEE operations
// instead of calling a C library.
//
// What: Asin, Acos, Atan and Atan2, the shared Atan2Core, the rational
// approximation AsinRatio, and pi and pi/2 as double-doubles.
//
// How: FreeBSD msun's e_asin.c, e_acos.c, s_atan.c and e_atan2.c: a rational
// approximation of asin on [0, 0.5] and the half-angle identity above it,
// and for atan a reduction to one of five intervals around 0, 0.5, 1, 1.5
// and infinity with atan's value there as a double-double. Every function
// is O(1), allocates nothing and never panics; a NaN result is Nan.

// pi and pi/2 as double-doubles.
const PiHi: f64 = f64.FromBits(0x400921FB54442D18) // 3.14159265358979311600e+00
const PiLo: f64 = f64.FromBits(0x3CA1A62633145C07) // 1.2246467991473531772e-16

// e_asin.c's rational approximation, p(t) / q(t) for asin's odd part.
const PS0: f64 = f64.FromBits(0x3FC5555555555555) // 1.66666666666666657415e-01
const PS1: f64 = f64.FromBits(0xBFD4D61203EB6F7D) // -3.25565818622400915405e-01
const PS2: f64 = f64.FromBits(0x3FC9C1550E884455) // 2.01212532134862925881e-01
const PS3: f64 = f64.FromBits(0xBFA48228B5688F3B) // -4.00555345006794114027e-02
const PS4: f64 = f64.FromBits(0x3F49EFE07501B288) // 7.91534994289814532176e-04
const PS5: f64 = f64.FromBits(0x3F023DE10DFDF709) // 3.47933107596021167570e-05
const QS1: f64 = f64.FromBits(0xC0033A271C8A2D4B) // -2.40339491173441421878e+00
const QS2: f64 = f64.FromBits(0x40002AE59C598AC8) // 2.02094576023350569471e+00
const QS3: f64 = f64.FromBits(0xBFE6066C1B8D0159) // -6.88283971605453293030e-01
const QS4: f64 = f64.FromBits(0x3FB3B8C5B12E9282) // 7.70381505559019352791e-02

// AsinRatio is the rational part p(t) / q(t) of asin on t = x^2 or
// t = (1 - |x|) / 2.
AsinRatio(t: f64): f64 {
    p := t * (PS0 + t * (PS1 + t * (PS2 + t * (PS3 + t * (PS4 + t * PS5)))))
    q := 1.0 + t * (QS1 + t * (QS2 + t * (QS3 + t * QS4)))
    return p / q
}

/// Asin is the arcsine of x, in [-pi/2, pi/2]: Nan outside [-1, 1] or for a
/// NaN. Faithfully rounded (within 1 ulp) but not correctly rounded, so its
/// results may change in a minor release. TestSTDMATH_TrueError: largest error
/// 0.762 ulp over 20,151 inputs, 624 of them not correctly rounded. O(1);
/// allocates nothing.
export Asin(x: f64): f64 {
    b := x.Bits()
    a := b & AbsMask64
    if a >= 0x3FF0000000000000 {
        if a == 0x3FF0000000000000 {
            return x * Pio2Hi + x * Pio2Lo // asin(1) = pi/2
        }
        return Nan
    }
    if a < 0x3FE0000000000000 {
        // |x| < 0.5
        if a < 0x3E50000000000000 {
            return x // |x| < 2^-26
        }
        return x + x * AsinRatio(x * x)
    }
    // 0.5 <= |x| < 1: asin(x) = pi/2 - 2 asin(sqrt((1 - |x|) / 2)).
    t := (1.0 - Abs(x)) * 0.5
    r := AsinRatio(t)
    s := Sqrt(t)
    var y := 0.0
    if a >= 0x3FEF333300000000 {
        // |x| > 0.975
        y = Pio2Hi - (2.0 * (s + s * r) - Pio2Lo)
    } else {
        w := f64.FromBits(s.Bits() & 0xFFFFFFFF00000000)
        c := (t - w * w) / (s + w)
        p := 2.0 * s * r - (Pio2Lo - 2.0 * c)
        q := PiOver4 - 2.0 * w
        y = PiOver4 - (p - q)
    }
    return if b & SignBit64 != 0 { -y } else { y }
}

/// Acos is the arccosine of x, in [0, pi]: Nan outside [-1, 1] or for a NaN.
/// Faithfully rounded (within 1 ulp) but not correctly rounded, so its results
/// may change in a minor release. TestSTDMATH_TrueError: largest error 0.829
/// ulp over 20,151 inputs, 794 of them not correctly rounded. O(1); allocates
/// nothing.
export Acos(x: f64): f64 {
    b := x.Bits()
    a := b & AbsMask64
    if a >= 0x3FF0000000000000 {
        if a == 0x3FF0000000000000 {
            return if b & SignBit64 != 0 { PiHi + 2.0 * Pio2Lo } else { 0.0 }
        }
        return Nan
    }
    if a < 0x3FE0000000000000 {
        // |x| < 0.5
        if a <= 0x3C60000000000000 {
            return Pio2Hi + Pio2Lo // |x| <= 2^-57
        }
        return Pio2Hi - (x - (Pio2Lo - x * AsinRatio(x * x)))
    }
    if b & SignBit64 != 0 {
        // x <= -0.5: acos(x) = pi - 2 asin(sqrt((1 + x) / 2)).
        z := (1.0 + x) * 0.5
        s := Sqrt(z)
        w := AsinRatio(z) * s - Pio2Lo
        return PiHi - 2.0 * (s + w)
    }
    // x >= 0.5: acos(x) = 2 asin(sqrt((1 - x) / 2)).
    z := (1.0 - x) * 0.5
    s := Sqrt(z)
    df := f64.FromBits(s.Bits() & 0xFFFFFFFF00000000)
    c := (z - df * df) / (s + df)
    w := AsinRatio(z) * s + c
    return 2.0 * (df + w)
}

// s_atan.c's atan at 0.5, 1, 1.5 and infinity, as hi + lo.
const AtanHi: [4]f64 = [
    f64.FromBits(0x3FDDAC670561BB4F), f64.FromBits(0x3FE921FB54442D18),
    f64.FromBits(0x3FEF730BD281F69B), f64.FromBits(0x3FF921FB54442D18),
]
const AtanLo: [4]f64 = [
    f64.FromBits(0x3C7A2B7F222F65E2), f64.FromBits(0x3C81A62633145C07),
    f64.FromBits(0x3C7007887AF0CBBD), f64.FromBits(0x3C91A62633145C07),
]

// s_atan.c's polynomial for atan on [-7/16, 7/16].
const AT0: f64 = f64.FromBits(0x3FD555555555550D)  // 3.33333333333329318027e-01
const AT1: f64 = f64.FromBits(0xBFC999999998EBC4)  // -1.99999999998764832476e-01
const AT2: f64 = f64.FromBits(0x3FC24924920083FF)  // 1.42857142725034663711e-01
const AT3: f64 = f64.FromBits(0xBFBC71C6FE231671)  // -1.11111104054623557880e-01
const AT4: f64 = f64.FromBits(0x3FB745CDC54C206E)  // 9.09088713343650656196e-02
const AT5: f64 = f64.FromBits(0xBFB3B0F2AF749A6D)  // -7.69187620504482999495e-02
const AT6: f64 = f64.FromBits(0x3FB10D66A0D03D51)  // 6.66107313738753120669e-02
const AT7: f64 = f64.FromBits(0xBFADDE2D52DEFD9A)  // -5.83357013379057348645e-02
const AT8: f64 = f64.FromBits(0x3FA97B4B24760DEB)  // 4.97687799461593236017e-02
const AT9: f64 = f64.FromBits(0xBFA2B4442C6A6C2F)  // -3.65315727442169155270e-02
const AT10: f64 = f64.FromBits(0x3F90AD3AE322DA11) // 1.62858201153657823623e-02

/// Atan is the arctangent of x, in [-pi/2, pi/2]: +-pi/2 for an infinity, Nan
/// for a NaN. Faithfully rounded (within 1 ulp) but not correctly rounded, so
/// its results may change in a minor release. TestSTDMATH_TrueError: largest
/// error 0.695 ulp over 20,173 inputs, 231 of them not correctly rounded. O(1);
/// allocates nothing.
export Atan(x: f64): f64 {
    b := x.Bits()
    a := b & AbsMask64
    neg := b & SignBit64 != 0
    if a >= 0x4410000000000000 {
        // |x| >= 2^66, an infinity or a NaN
        if a > ExpMask64 {
            return Nan
        }
        return if neg { -AtanHi[3] - AtanLo[3] } else { AtanHi[3] + AtanLo[3] }
    }
    var id := -1
    var v := x
    if a < 0x3FDC000000000000 {
        // |x| < 0.4375
        if a < 0x3E40000000000000 {
            return x // |x| < 2^-27
        }
    } else {
        v = Abs(x)
        if a < 0x3FF3000000000000 {
            // |x| < 1.1875
            if a < 0x3FE6000000000000 {
                id = 0 // 7/16 <= |x| < 11/16
                v = (2.0 * v - 1.0) / (2.0 + v)
            } else {
                id = 1 // 11/16 <= |x| < 19/16
                v = (v - 1.0) / (v + 1.0)
            }
        } else if a < 0x4003800000000000 {
            id = 2 // 19/16 <= |x| < 39/16
            v = (v - 1.5) / (1.0 + 1.5 * v)
        } else {
            id = 3 // 39/16 <= |x| < 2^66
            v = -1.0 / v
        }
    }
    z := v * v
    w := z * z
    s1 := z * (AT0 + w * (AT2 + w * (AT4 + w * (AT6 + w * (AT8 + w * AT10)))))
    s2 := w * (AT1 + w * (AT3 + w * (AT5 + w * (AT7 + w * AT9))))
    if id < 0 {
        return v - v * (s1 + s2)
    }
    r := AtanHi[id] - ((v * (s1 + s2) - AtanLo[id]) - v)
    return if neg { -r } else { r }
}

/// Atan2 is the angle of the point (x, y) from the positive x axis, in [-pi,
/// pi], with C99's special cases: a zero y keeps its sign, a zero y with a
/// negative x gives +-pi, and a NaN in either gives Nan. Faithfully rounded
/// (within 1 ulp) but not correctly rounded, so its results may change in a
/// minor release. TestSTDMATH_TrueError: largest error 0.557 ulp over 21,681
/// inputs, 7 of them not correctly rounded. Over the 3,146 adversarial and
/// random inputs of TestSTDMATH_Edges, largest error 0.500 ulp. O(1);
/// allocates nothing.
export Atan2(y: f64, x: f64): f64 {
    if IsNan(x) || IsNan(y) {
        return Nan
    }
    bx := x.Bits()
    by := y.Bits()
    ynegative := by & SignBit64 != 0
    xnegative := bx & SignBit64 != 0
    ax := bx & AbsMask64
    ay := by & AbsMask64
    if ay == 0 {
        if !xnegative {
            return y
        }
        return if ynegative { -PiHi } else { PiHi }
    }
    if ax == 0 {
        return if ynegative { -Pio2Hi } else { Pio2Hi }
    }
    if ax == ExpMask64 {
        if ay == ExpMask64 {
            r := if xnegative { 3.0 * PiOver4 } else { PiOver4 }
            return if ynegative { -r } else { r }
        }
        if !xnegative {
            return if ynegative { -0.0 } else { 0.0 }
        }
        return if ynegative { -PiHi } else { PiHi }
    }
    if ay == ExpMask64 {
        return if ynegative { -Pio2Hi } else { Pio2Hi }
    }
    // Both with biased exponents in [512, 1535] (2^-511 <= |x|, |y| <
    // 2^513: normal; 1024 wide, so one or of the two offsets tests both):
    // k from the exponent fields, and Atan2Core on |y| and |x| themselves.
    // The general path below calls it on Y = |y| / s and X = |x| / s, s
    // the power of two FrExp takes out of x (both exact), with the same k.
    // Atan2Core gives the same bits on both pairs. qh = Y / X and the
    // remainder over X are quotients of two operands scaled alike, so the
    // same exact ratio. Every other step before AtanDD (TwoProd(qh, X):
    // 134217729 X, X's split and the four partial products; Y - p.hi;
    // minus p.lo) is a product of one scaled operand by an unscaled one,
    // or a sum or difference of such, so its exact value is s times the
    // general path's, and rounding commutes with a power of two unless a
    // result overflows or is subnormal. None is here: the largest is
    // 134217729 X < 2^541, and each nonzero one is a multiple of ulp(qh)
    // ulp(X) >= 2^-113 2^-563 = 2^-676 (|qh| >= 2^-61 as |k| <= 60), far
    // above 2^-1022. So AtanDD, and the pi - atan step, see the same qh
    // and remainder quotient. Zeros, infinities and NaNs are handled above,
    // and subnormals keep the general path.
    if ((ax -% 0x2000000000000000) | (ay -% 0x2000000000000000)) < 0x4000000000000000 {
        kk := Trunc[int](ay >> 52) - Trunc[int](ax >> 52)
        if kk > 60 {
            return if ynegative { -Pio2Hi } else { Pio2Hi }
        }
        if kk < -60 {
            if !xnegative {
                return y / x
            }
            return if ynegative { -PiHi } else { PiHi }
        }
        zz := Atan2Core(f64.FromBits(ay), f64.FromBits(ax), xnegative).hi
        return if ynegative { -zz } else { zz }
    }
    // |x| = fx 2^ex and |y| = fy 2^ey, fx and fy in [0.5, 1): the exponent
    // difference decides whether y / x is beyond atan's resolution.
    fx := FrExp(f64.FromBits(ax))
    fy := FrExp(f64.FromBits(ay))
    k := fy.exp - fx.exp
    if k > 60 {
        // |y / x| > 2^59: +-pi/2 whatever x's sign
        return if ynegative { -Pio2Hi } else { Pio2Hi }
    }
    if k < -60 {
        // |y / x| < 2^-59: y / x, or +-pi with x < 0
        if !xnegative {
            return y / x
        }
        return if ynegative { -PiHi } else { PiHi }
    }
    // |y / x| = Y / X with both scaled exactly into [2^-61, 2^60].
    z := Atan2Core(LdExp(fy.frac, k), fx.frac, xnegative).hi
    return if ynegative { -z } else { z }
}

// Atan2Core is |Atan2| for ys = |y| and xs = |x|, scaled into [2^-61,
// 2^60] (Atan2's general case) or as they are, in [2^-511, 2^513) with
// their exponents at most 60 apart (its fast path), as hi + lo with hi the
// rounded result: the
// quotient as qh + its remainder over xs into AtanDD, and pi - atan as a
// double-double when x is negative. Atan2F32 reads lo's sign (mid32.mk).
Atan2Core(ys: f64, xs: f64, xnegative: bool): (hi: f64, lo: f64) {
    qh := ys / xs
    p := TwoProd(qh, xs)
    // ys - p.hi is exact (Sterbenz).
    a := AtanDD(qh, ((ys - p.hi) - p.lo) / xs)
    if !xnegative {
        return a
    }
    s := TwoSum(PiHi, -a.hi)
    return FastTwoSum(s.hi, (s.lo + PiLo) - a.lo)
}

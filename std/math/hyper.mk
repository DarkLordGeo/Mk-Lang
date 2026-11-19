// Why: msun's hyperbolic functions combine two rounded results and measured
// 1.03 to 1.44 ulps (TestSTDMATH_TrueError); these stay below 1 ulp.
//
// What: Sinh, Cosh, Tanh, Asinh, Acosh and Atanh.
//
// How: each is written over dd.mk's double-double kernels and rounds once:
// Sinh and Cosh as (e^x -+ e^-x) / 2 from ExpDD, with odd Taylor series
// where they cancel; Tanh as t / (t + 2), t = e^2x - 1; Asinh and Acosh as
// ln(x + sqrt(x^2 +- 1)) with the argument and its square root carried as
// hi + lo into LogDD; Atanh as (log1p(x) - log1p(-x)) / 2 through LogDD.
// All O(1), no allocation, no panic; a NaN result is Nan.

// Pow2 is 2^k for -1022 <= k <= 1023, exactly.
Pow2(k: int): f64 {
    return f64.FromBits(Trunc[u64](k + 1023) << 52)
}

// Past SinhOver, Sinh and Cosh overflow (e^x / 2 above the largest f64).
const SinhOver: f64 = f64.FromBits(0x408633CE8FB9F87D) // 710.4758600739439

// Sinh's Taylor series below 0.5: sinh x = x + x^3 (1/3! + x^2/5! + ... +
// x^14/17!), truncation below 2^-66 of x.
const SinhC3: f64 = f64.FromBits(0x3FC5555555555555)  // 1/3!
const SinhC5: f64 = f64.FromBits(0x3F81111111111111)  // 1/5!
const SinhC7: f64 = f64.FromBits(0x3F2A01A01A01A01A)  // 1/7!
const SinhC9: f64 = f64.FromBits(0x3EC71DE3A556C734)  // 1/9!
const SinhC11: f64 = f64.FromBits(0x3E5AE64567F544E4) // 1/11!
const SinhC13: f64 = f64.FromBits(0x3DE6124613A86D09) // 1/13!
const SinhC15: f64 = f64.FromBits(0x3D6AE7F3E733B81F) // 1/15!
const SinhC17: f64 = f64.FromBits(0x3CE952C77030AD4A) // 1/17!

// Tanh's Taylor series below 0.125: tanh x = x + x^3 (c3 + x^2 c5 + ... +
// x^14 c17), c(2n-1) = 2^2n (2^2n - 1) B(2n) / (2n)!, truncation below
// 2^-66 of x.
const TanhC3: f64 = f64.FromBits(0xBFD5555555555555)  // -1/3
const TanhC5: f64 = f64.FromBits(0x3FC1111111111111)  // 2/15
const TanhC7: f64 = f64.FromBits(0xBFABA1BA1BA1BA1C)  // -17/315
const TanhC9: f64 = f64.FromBits(0x3F9664F4882C10FA)  // 62/2835
const TanhC11: f64 = f64.FromBits(0xBF8226E355E6C23D) // -1382/155925
const TanhC13: f64 = f64.FromBits(0x3F6D6D3D0E157DE0) // 21844/6081075
const TanhC15: f64 = f64.FromBits(0xBF57DA36452B75E3) // -929569/638512875
const TanhC17: f64 = f64.FromBits(0x3F43558248036744) // 6404582/10854718875

/// Sinh is the hyperbolic sine of x: an infinity keeps its sign, Nan for a NaN.
/// Correctly rounded on every input tested, but not proved so for every input,
/// so its results may change in a minor release. TestSTDMATH_TrueError:
/// largest error 0.500 ulp over 20,185 inputs, every one correctly rounded.
/// Over the 3,054 adversarial and random inputs of TestSTDMATH_Edges, largest
/// error 0.543 ulp. O(1); allocates nothing.
export Sinh(x: f64): f64 {
    b := x.Bits()
    a := b & AbsMask64
    if a >= ExpMask64 {
        return if a > ExpMask64 { Nan } else { x }
    }
    if a < 0x3FE0000000000000 {
        // |x| < 0.5
        if a < 0x3E30000000000000 {
            return x // |x| < 2^-28, a zero's sign kept
        }
        s := x * x
        p := SinhC3 + s * (SinhC5 + s * (SinhC7 + s * (SinhC9 + s * (SinhC11 + s * (SinhC13 + s * (SinhC15 + s * SinhC17))))))
        return x + x * s * p
    }
    ax := Abs(x)
    if ax > SinhOver {
        return if b & SignBit64 != 0 { -Inf } else { Inf }
    }
    e := ExpDD(ax)
    f := ExpDD(-ax)
    var r := 0.0
    if ax < 23.0 {
        // Every scale is a normal power of two: a product by it is exact,
        // as LdExp is, without LdExp's cases.
        s := Pow2(f.k - e.k)
        d := TwoSum(e.hi, -(f.hi * s))
        r = (d.hi + (d.lo + (e.lo - f.lo * s))) * Pow2(e.k - 1)
    } else {
        d := TwoSum(e.hi, -LdExp(f.hi, f.k - e.k))
        r = LdExp(d.hi + (d.lo + (e.lo - LdExp(f.lo, f.k - e.k))), e.k - 1)
    }
    return if b & SignBit64 != 0 { -r } else { r }
}

/// Cosh is the hyperbolic cosine of x: inf for an infinity, Nan for a NaN.
/// Correctly rounded on every input tested, but not proved so for every input,
/// so its results may change in a minor release. TestSTDMATH_TrueError:
/// largest error 0.500 ulp over 20,185 inputs, every one correctly rounded.
/// Over the 3,054 adversarial and random inputs of TestSTDMATH_Edges, largest
/// error 0.500 ulp. O(1); allocates nothing.
export Cosh(x: f64): f64 {
    a := x.Bits() & AbsMask64
    if a >= ExpMask64 {
        return if a > ExpMask64 { Nan } else { Inf }
    }
    ax := Abs(x)
    if ax > SinhOver {
        return Inf
    }
    e := ExpDD(ax)
    f := ExpDD(-ax)
    if ax < 23.0 {
        // As Sinh: products by normal powers of two, exact.
        s := Pow2(f.k - e.k)
        d := TwoSum(e.hi, f.hi * s)
        return (d.hi + (d.lo + (e.lo + f.lo * s))) * Pow2(e.k - 1)
    }
    d := TwoSum(e.hi, LdExp(f.hi, f.k - e.k))
    return LdExp(d.hi + (d.lo + (e.lo + LdExp(f.lo, f.k - e.k))), e.k - 1)
}

/// Tanh is the hyperbolic tangent of x: +-1 for an infinity, Nan for a NaN.
/// Correctly rounded on every input tested, but not proved so for every input,
/// so its results may change in a minor release. TestSTDMATH_TrueError:
/// largest error 0.500 ulp over 20,193 inputs, every one correctly rounded.
/// Over the 3,036 adversarial and random inputs of TestSTDMATH_Edges, largest
/// error 0.500 ulp. O(1); allocates nothing.
export Tanh(x: f64): f64 {
    b := x.Bits()
    a := b & AbsMask64
    if a > ExpMask64 {
        return Nan
    }
    if a < 0x3FC0000000000000 {
        // |x| < 0.125
        if a < 0x3E30000000000000 {
            return x // |x| < 2^-28, a zero's sign kept
        }
        s := x * x
        p := TanhC3 + s * (TanhC5 + s * (TanhC7 + s * (TanhC9 + s * (TanhC11 + s * (TanhC13 + s * (TanhC15 + s * TanhC17))))))
        return x + x * s * p
    }
    var z := 1.0
    if a < 0x4036000000000000 {
        // |x| < 22: t = e^2|x| - 1 as hi + lo, then t / (t + 2).
        e := ExpDD(2.0 * Abs(x))
        t := TwoSum(LdExp(e.hi, e.k), -1.0)
        tl := t.lo + LdExp(e.lo, e.k)
        d := TwoSum(t.hi, 2.0)
        dl := d.lo + tl
        q := t.hi / d.hi
        p := TwoProd(q, d.hi)
        // t.hi - p.hi is exact (Sterbenz).
        z = q + ((((t.hi - p.hi) - p.lo) + tl) - q * dl) / d.hi
    }
    return if b & SignBit64 != 0 { -z } else { z }
}

/// Asinh is the inverse hyperbolic sine of x: an infinity keeps its sign, Nan
/// for a NaN. Correctly rounded on every input tested, but not proved so for
/// every input, so its results may change in a minor release.
/// TestSTDMATH_TrueError: largest error 0.500 ulp over 20,173 inputs, every
/// one correctly rounded. Over the 3,040 adversarial and random inputs of
/// TestSTDMATH_Edges, largest error 0.500 ulp. O(1); allocates nothing.
export Asinh(x: f64): f64 {
    b := x.Bits()
    a := b & AbsMask64
    if a >= ExpMask64 {
        return if a > ExpMask64 { Nan } else { x }
    }
    if a < 0x3E30000000000000 {
        return x // |x| < 2^-28
    }
    t := Abs(x)
    var w := 0.0
    if a >= 0x41B0000000000000 {
        // |x| >= 2^28: ln(2|x|), the rest below 2^-58.
        l := LogDD(t, 0.0)
        s := TwoSum(l.hi, Ln2Near)
        w = s.hi + (s.lo + (l.lo + Ln2Tail))
    } else {
        // u = |x| + sqrt(x^2 + 1) as hi + lo, the root by one correction.
        sq := TwoProd(t, t)
        v := TwoSum(sq.hi, 1.0)
        vl := v.lo + sq.lo
        r := Sqrt(v.hi)
        rr := TwoProd(r, r)
        rl := (((v.hi - rr.hi) - rr.lo) + vl) / (2.0 * r)
        u := TwoSum(t, r)
        l := LogDD(u.hi, u.lo + rl)
        w = l.hi
    }
    return if b & SignBit64 != 0 { -w } else { w }
}

/// Acosh is the inverse hyperbolic cosine of x, for x >= 1: 0 at 1, inf for
/// inf, Nan below 1 or for a NaN. Correctly rounded on every input tested, but
/// not proved so for every input, so its results may change in a minor release.
/// TestSTDMATH_TrueError: largest error 0.500 ulp over 20,161 inputs, every
/// one correctly rounded. Over the 3,119 adversarial and random inputs of
/// TestSTDMATH_Edges, largest error 0.500 ulp. O(1); allocates nothing.
export Acosh(x: f64): f64 {
    b := x.Bits()
    if b < 0x3FF0000000000000 || b > ExpMask64 {
        return Nan // x < 1 (any negative included) or a NaN
    }
    if b == ExpMask64 {
        return x
    }
    if b == 0x3FF0000000000000 {
        return 0.0
    }
    if b >= 0x41B0000000000000 {
        // x >= 2^28: ln(2x), the rest below 2^-58.
        l := LogDD(x, 0.0)
        s := TwoSum(l.hi, Ln2Near)
        return s.hi + (s.lo + (l.lo + Ln2Tail))
    }
    // u = x + sqrt(x^2 - 1) as hi + lo; x^2 - 1 is exact as hi + lo.
    sq := TwoProd(x, x)
    v := TwoSum(sq.hi, -1.0)
    vl := v.lo + sq.lo
    r := Sqrt(v.hi)
    rr := TwoProd(r, r)
    rl := (((v.hi - rr.hi) - rr.lo) + vl) / (2.0 * r)
    u := TwoSum(x, r)
    return LogDD(u.hi, u.lo + rl).hi
}

/// Atanh is the inverse hyperbolic tangent of x: +-inf at +-1, Nan beyond them
/// or for a NaN. Correctly rounded on every input tested, but not proved so for
/// every input, so its results may change in a minor release.
/// TestSTDMATH_TrueError: largest error 0.500 ulp over 20,153 inputs, every
/// one correctly rounded. Over the 3,130 adversarial and random inputs of
/// TestSTDMATH_Edges, largest error 0.500 ulp. O(1); allocates nothing.
export Atanh(x: f64): f64 {
    b := x.Bits()
    a := b & AbsMask64
    if a > 0x3FF0000000000000 {
        return Nan
    }
    if a == 0x3FF0000000000000 {
        return if b & SignBit64 != 0 { -Inf } else { Inf }
    }
    if a < 0x3E30000000000000 {
        return x // |x| < 2^-28
    }
    ax := Abs(x)
    up := TwoSum(1.0, ax)
    dn := TwoSum(1.0, -ax)
    p := LogDD(up.hi, up.lo)
    m := LogDD(dn.hi, dn.lo)
    d := TwoSum(p.hi, -m.hi)
    t := 0.5 * (d.hi + (d.lo + (p.lo - m.lo)))
    return if b & SignBit64 != 0 { -t } else { t }
}

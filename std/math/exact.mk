// Why: the exact f64 operations (rounding, classifying, splitting) must
// give the same bits on every target and in the evaluator, so they cannot
// depend on the hardware's rounding instructions.
//
// What: the constants (Pi, E, Ln2, Inf, Nan and the bit masks), the
// classifiers IsNan, IsInf, IsFinite, and Floor, Ceil, RoundToZero, Round,
// RoundEven, Fract, FrExp, LdExp, CopySign, NextAfter and Mod.
//
// How: each function works on the IEEE 754 encoding with integer
// operations, so its result is exact; a NaN it returns is Nan's bits. The
// algorithms are FreeBSD msun's s_floor.c, s_ceil.c, s_trunc.c, s_frexp.c,
// s_nextafter.c and Go's math.Round and RoundToEven, restated on u64.
// Nothing allocates and nothing panics.

import std.raw

/// Pi, the f64 nearest to π.
export const Pi: f64 = f64.FromBits(0x400921FB54442D18)

/// E, the f64 nearest to Euler's number e.
export const E: f64 = f64.FromBits(0x4005BF0A8B145769)

/// Ln2, the f64 nearest to the natural logarithm of 2.
export const Ln2: f64 = f64.FromBits(0x3FE62E42FEFA39EF)

/// Sqrt2, the f64 nearest to the square root of 2.
export const Sqrt2: f64 = f64.FromBits(0x3FF6A09E667F3BCD)

/// Inf, positive infinity.
export const Inf: f64 = f64.FromBits(0x7FF0000000000000)

/// Nan, the quiet NaN 0x7FF8000000000000 that every std.math function
/// returns for a NaN result.
export const Nan: f64 = f64.FromBits(0x7FF8000000000000)

// The fields of the f64 encoding.
const SignBit64: u64 = 0x8000000000000000
const AbsMask64: u64 = 0x7FFFFFFFFFFFFFFF
const ExpMask64: u64 = 0x7FF0000000000000
const FracMask64: u64 = 0x000FFFFFFFFFFFFF
const NanBits64: u64 = 0x7FF8000000000000

/// IsNan reports whether x is a NaN. O(1); allocates nothing.
export IsNan(x: f64): bool {
    return x.Bits() & AbsMask64 > ExpMask64
}

/// IsInf reports whether x is +inf or -inf. O(1); allocates nothing.
export IsInf(x: f64): bool {
    return x.Bits() & AbsMask64 == ExpMask64
}

/// IsFinite reports whether x is neither an infinity nor a NaN. O(1);
/// allocates nothing.
export IsFinite(x: f64): bool {
    return x.Bits() & AbsMask64 < ExpMask64
}

/// CopySign is x's magnitude with sign's sign bit, x's other bits kept, a
/// NaN's payload included. Exact. O(1); allocates nothing.
export CopySign(x: f64, sign: f64): f64 {
    return f64.FromBits(x.Bits() & AbsMask64 | sign.Bits() & SignBit64)
}

/// RoundToZero rounds x toward zero to an integer, keeping its sign (-0.5 gives
/// -0); an infinity is itself and a NaN is Nan. Exact. O(1); allocates
/// nothing.
export RoundToZero(x: f64): f64 {
    b := x.Bits()
    e := Trunc[int]((b >> 52) & 0x7FF) - 1023
    if e >= 52 {
        return if e == 1024 && b & FracMask64 != 0 { Nan } else { x }
    }
    if e < 0 {
        return f64.FromBits(b & SignBit64)
    }
    return f64.FromBits(b & ~(FracMask64 >> e))
}

/// Floor is the greatest integer not above x, keeping a zero's sign; an
/// infinity is itself and a NaN is Nan. Exact. O(1); allocates nothing.
export Floor(x: f64): f64 {
    var b := x.Bits()
    e := Trunc[int]((b >> 52) & 0x7FF) - 1023
    if e >= 52 {
        return if e == 1024 && b & FracMask64 != 0 { Nan } else { x }
    }
    if e < 0 {
        if b & AbsMask64 == 0 || b & SignBit64 == 0 {
            return f64.FromBits(b & SignBit64)
        }
        return -1.0
    }
    m := FracMask64 >> e
    if b & m == 0 {
        return x
    }
    if b & SignBit64 != 0 {
        b += m // a carry out of the fraction raises the exponent, as it should
    }
    return f64.FromBits(b & ~m)
}

/// Ceil is the least integer not below x, keeping a zero's sign (-0.5
/// gives -0); an infinity is itself and a NaN is Nan. Exact. O(1);
/// allocates nothing.
export Ceil(x: f64): f64 {
    var b := x.Bits()
    e := Trunc[int]((b >> 52) & 0x7FF) - 1023
    if e >= 52 {
        return if e == 1024 && b & FracMask64 != 0 { Nan } else { x }
    }
    if e < 0 {
        if b & AbsMask64 == 0 || b & SignBit64 != 0 {
            return f64.FromBits(b & SignBit64)
        }
        return 1.0
    }
    m := FracMask64 >> e
    if b & m == 0 {
        return x
    }
    if b & SignBit64 == 0 {
        b += m
    }
    return f64.FromBits(b & ~m)
}

/// Round rounds x to the nearest integer, a half away from zero, keeping
/// its sign; an infinity is itself and a NaN is Nan. Exact. O(1);
/// allocates nothing.
export Round(x: f64): f64 {
    var b := x.Bits()
    e := Trunc[int]((b >> 52) & 0x7FF) - 1023
    if e >= 52 {
        return if e == 1024 && b & FracMask64 != 0 { Nan } else { x }
    }
    if e < -1 {
        return f64.FromBits(b & SignBit64)
    }
    if e == -1 {
        return f64.FromBits(b & SignBit64 | 0x3FF0000000000000)
    }
    b += 0x0008000000000000 >> e
    return f64.FromBits(b & ~(FracMask64 >> e))
}

/// RoundEven rounds x to the nearest integer, a half to the even one,
/// keeping its sign; an infinity is itself and a NaN is Nan. Exact. O(1);
/// allocates nothing.
export RoundEven(x: f64): f64 {
    var b := x.Bits()
    e := Trunc[int]((b >> 52) & 0x7FF) - 1023
    if e >= 52 {
        return if e == 1024 && b & FracMask64 != 0 { Nan } else { x }
    }
    if e >= 0 {
        // Half an ulp of the result less one, plus the result's lowest bit.
        b += (0x0007FFFFFFFFFFFF + ((b >> (52 - e)) & 1)) >> e
        return f64.FromBits(b & ~(FracMask64 >> e))
    }
    if e == -1 && b & FracMask64 != 0 {
        return f64.FromBits(b & SignBit64 | 0x3FF0000000000000)
    }
    return f64.FromBits(b & SignBit64)
}

/// Fract is x - RoundToZero(x), the fractional part, with x's sign (Fract(-3.0)
/// is -0); Nan for an infinity or a NaN. Exact. O(1); allocates nothing.
export Fract(x: f64): f64 {
    b := x.Bits()
    e := Trunc[int]((b >> 52) & 0x7FF) - 1023
    if e == 1024 {
        return Nan
    }
    if e >= 52 {
        return f64.FromBits(b & SignBit64)
    }
    if e < 0 {
        return x
    }
    return CopySign(x - RoundToZero(x), x)
}

/// Mod is the exact remainder of x / y truncated toward zero: x - n*y for the
/// integer n of that quotient, with x's sign. Nan when x is infinite, y is
/// zero, or either is a NaN; x when y is infinite and x finite. O(ex - ey) in
/// the exponent difference, one integer division per 11 bits; allocates
/// nothing.
export Mod(x: f64, y: f64): f64 {
    return Rem64(x, y)
}

/// FrExp splits x into frac in [0.5, 1) and exp with x = frac * 2^exp,
/// frac keeping x's sign; a zero or an infinity is (x, 0) and a NaN is
/// (Nan, 0). Exact. O(1); allocates nothing.
export FrExp(x: f64): (frac: f64, exp: int) {
    var b := x.Bits()
    var e := Trunc[int]((b >> 52) & 0x7FF)
    if e == 0x7FF {
        return (frac: if b & FracMask64 != 0 { Nan } else { x }, exp: 0)
    }
    if e == 0 {
        if b & AbsMask64 == 0 {
            return (frac: x, exp: 0)
        }
        // Subnormal: shift the leading one to bit 52.
        s := raw.Clz64(b & FracMask64) - 11
        b = b & SignBit64 | (b & FracMask64) << s
        e = 1 - s
        return (frac: f64.FromBits(b & SignBit64 | 0x3FE0000000000000 | b & FracMask64), exp: e - 1022)
    }
    return (frac: f64.FromBits(b & ~ExpMask64 | 0x3FE0000000000000), exp: e - 1022)
}

/// LdExp is frac * 2^exp rounded once, to nearest with ties to even, so it is
/// exact unless the result is subnormal or out of range: an infinity past the
/// largest f64, a zero of frac's sign below the least. A zero or an infinity is
/// itself, and a NaN gives Nan. O(1); allocates nothing.
export LdExp(frac: f64, exp: int): f64 {
    b := frac.Bits()
    sign := b & SignBit64
    var e := Trunc[int]((b >> 52) & 0x7FF)
    var m := b & FracMask64
    if e == 0x7FF {
        return if m != 0 { Nan } else { frac }
    }
    if e == 0 {
        if m == 0 {
            return frac
        }
        s := raw.Clz64(m) - 11
        m = m << s
        e = 1 - s
    } else {
        m = m | 0x0010000000000000
    }
    // Clamped, so the sum cannot overflow; past either bound the result is
    // already an infinity or a zero.
    n := if exp > 4096 { 4096 } else if exp < -4096 { -4096 } else { exp }
    r := e + n
    if r >= 0x7FF {
        return f64.FromBits(sign | ExpMask64)
    }
    if r >= 1 {
        return f64.FromBits(sign | Trunc[u64](r) << 52 | m & FracMask64)
    }
    // Subnormal or zero: shift right by 1 - r, rounding to nearest even.
    s := 1 - r
    if s > 54 {
        return f64.FromBits(sign)
    }
    var q := m >> s
    rest := m & ((1 << s) - 1)
    half := Trunc[u64](1) << (s - 1)
    if rest > half || rest == half && q & 1 == 1 {
        q += 1 // a carry to 2^52 is the least normal, encoded correctly
    }
    return f64.FromBits(sign | q)
}

/// NextAfter is the f64 next to x in the direction of toward: x when they
/// are equal, Nan when either is a NaN, the least subnormal of toward's
/// sign from a zero. Exact. O(1); allocates nothing.
export NextAfter(x: f64, toward: f64): f64 {
    if IsNan(x) || IsNan(toward) {
        return Nan
    }
    if x == toward {
        return x
    }
    if x == 0.0 {
        return CopySign(f64.FromBits(1), toward)
    }
    b := x.Bits()
    if (toward > x) == (x > 0.0) {
        return f64.FromBits(b + 1)
    }
    return f64.FromBits(b - 1)
}

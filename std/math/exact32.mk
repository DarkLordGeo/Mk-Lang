// Why: the f32 forms of the exact operations need the same guarantee as
// the f64 ones: exact results with the same bits everywhere.
//
// What: NanF32, the f32 masks, IsNanF32, IsInfF32, IsFiniteF32, FloorF32,
// CeilF32, RoundToZeroF32, RoundF32, RoundEvenF32, FractF32, FrExpF32,
// LdExpF32, CopySignF32, NextAfterF32, ModF32 and Narrow.
//
// How: exact.mk's algorithms on the f32 encoding (8 exponent bits, bias
// 127, 23 fraction bits) with integer operations; a NaN returned is
// NanF32's bits, 0x7FC00000. Nothing allocates and nothing panics.

import std.raw

const SignBit32: u32 = 0x80000000
const AbsMask32: u32 = 0x7FFFFFFF
const ExpMask32: u32 = 0x7F800000
const FracMask32: u32 = 0x007FFFFF

// NanF32 is the f32 NaN std.math returns.
const NanF32: f32 = f32.FromBits(0x7FC00000)

/// IsNanF32 reports whether x is a NaN. O(1); allocates nothing.
export IsNanF32(x: f32): bool {
    return x.Bits() & AbsMask32 > ExpMask32
}

/// IsInfF32 reports whether x is +inf or -inf. O(1); allocates nothing.
export IsInfF32(x: f32): bool {
    return x.Bits() & AbsMask32 == ExpMask32
}

/// IsFiniteF32 reports whether x is neither an infinity nor a NaN. O(1);
/// allocates nothing.
export IsFiniteF32(x: f32): bool {
    return x.Bits() & AbsMask32 < ExpMask32
}

/// CopySignF32 is x's magnitude with sign's sign bit, x's other bits kept.
/// Exact. O(1); allocates nothing.
export CopySignF32(x: f32, sign: f32): f32 {
    return f32.FromBits(x.Bits() & AbsMask32 | sign.Bits() & SignBit32)
}

/// RoundToZeroF32 rounds x toward zero to an integer, keeping its sign; an
/// infinity is itself and a NaN is Nan's f32 bits. Exact. O(1); allocates
/// nothing.
export RoundToZeroF32(x: f32): f32 {
    b := x.Bits()
    e := Trunc[int]((b >> 23) & 0xFF) - 127
    if e >= 23 {
        return if e == 128 && b & FracMask32 != 0 { NanF32 } else { x }
    }
    if e < 0 {
        return f32.FromBits(b & SignBit32)
    }
    return f32.FromBits(b & ~(FracMask32 >> e))
}

/// FloorF32 is the greatest integer not above x, keeping a zero's sign.
/// Exact. O(1); allocates nothing.
export FloorF32(x: f32): f32 {
    var b := x.Bits()
    e := Trunc[int]((b >> 23) & 0xFF) - 127
    if e >= 23 {
        return if e == 128 && b & FracMask32 != 0 { NanF32 } else { x }
    }
    if e < 0 {
        if b & AbsMask32 == 0 || b & SignBit32 == 0 {
            return f32.FromBits(b & SignBit32)
        }
        return -1.0
    }
    m := FracMask32 >> e
    if b & m == 0 {
        return x
    }
    if b & SignBit32 != 0 {
        b += m
    }
    return f32.FromBits(b & ~m)
}

/// CeilF32 is the least integer not below x, keeping a zero's sign. Exact.
/// O(1); allocates nothing.
export CeilF32(x: f32): f32 {
    var b := x.Bits()
    e := Trunc[int]((b >> 23) & 0xFF) - 127
    if e >= 23 {
        return if e == 128 && b & FracMask32 != 0 { NanF32 } else { x }
    }
    if e < 0 {
        if b & AbsMask32 == 0 || b & SignBit32 != 0 {
            return f32.FromBits(b & SignBit32)
        }
        return 1.0
    }
    m := FracMask32 >> e
    if b & m == 0 {
        return x
    }
    if b & SignBit32 == 0 {
        b += m
    }
    return f32.FromBits(b & ~m)
}

/// RoundF32 rounds x to the nearest integer, a half away from zero,
/// keeping its sign. Exact. O(1); allocates nothing.
export RoundF32(x: f32): f32 {
    var b := x.Bits()
    e := Trunc[int]((b >> 23) & 0xFF) - 127
    if e >= 23 {
        return if e == 128 && b & FracMask32 != 0 { NanF32 } else { x }
    }
    if e < -1 {
        return f32.FromBits(b & SignBit32)
    }
    if e == -1 {
        return f32.FromBits(b & SignBit32 | 0x3F800000)
    }
    b += 0x00400000 >> e
    return f32.FromBits(b & ~(FracMask32 >> e))
}

/// RoundEvenF32 rounds x to the nearest integer, a half to the even one,
/// keeping its sign. Exact. O(1); allocates nothing.
export RoundEvenF32(x: f32): f32 {
    var b := x.Bits()
    e := Trunc[int]((b >> 23) & 0xFF) - 127
    if e >= 23 {
        return if e == 128 && b & FracMask32 != 0 { NanF32 } else { x }
    }
    if e >= 0 {
        b += (0x003FFFFF + ((b >> (23 - e)) & 1)) >> e
        return f32.FromBits(b & ~(FracMask32 >> e))
    }
    if e == -1 && b & FracMask32 != 0 {
        return f32.FromBits(b & SignBit32 | 0x3F800000)
    }
    return f32.FromBits(b & SignBit32)
}

/// FractF32 is x - RoundToZeroF32(x) with x's sign; Nan's f32 bits for an
/// infinity or a NaN. Exact. O(1); allocates nothing.
export FractF32(x: f32): f32 {
    b := x.Bits()
    e := Trunc[int]((b >> 23) & 0xFF) - 127
    if e == 128 {
        return NanF32
    }
    if e >= 23 {
        return f32.FromBits(b & SignBit32)
    }
    if e < 0 {
        return x
    }
    return CopySignF32(x - RoundToZeroF32(x), x)
}

/// ModF32 is the exact remainder of x / y truncated toward zero, with x's sign.
/// Nan's f32 bits when x is infinite, y is zero, or either is a NaN; x when y
/// is infinite and x finite. O(ex - ey) in the exponent difference, one integer
/// division per 40 bits; allocates nothing.
export ModF32(x: f32, y: f32): f32 {
    return Rem32(x, y)
}

/// FrExpF32 splits x into frac in [0.5, 1) and exp with x = frac * 2^exp;
/// a zero or an infinity is (x, 0) and a NaN is (Nan, 0). Exact. O(1);
/// allocates nothing.
export FrExpF32(x: f32): (frac: f32, exp: int) {
    var b := x.Bits()
    var e := Trunc[int]((b >> 23) & 0xFF)
    if e == 0xFF {
        return (frac: if b & FracMask32 != 0 { NanF32 } else { x }, exp: 0)
    }
    if e == 0 {
        if b & AbsMask32 == 0 {
            return (frac: x, exp: 0)
        }
        s := raw.Clz64(u64(b & FracMask32)) - 40
        b = b & SignBit32 | (b & FracMask32) << s
        e = 1 - s
        return (frac: f32.FromBits(b & SignBit32 | 0x3F000000 | b & FracMask32), exp: e - 126)
    }
    return (frac: f32.FromBits(b & ~ExpMask32 | 0x3F000000), exp: e - 126)
}

/// LdExpF32 is frac * 2^exp rounded once, to nearest with ties to even:
/// exact unless the result is subnormal or out of range. O(1); allocates
/// nothing.
export LdExpF32(frac: f32, exp: int): f32 {
    b := frac.Bits()
    sign := b & SignBit32
    var e := Trunc[int]((b >> 23) & 0xFF)
    var m := b & FracMask32
    if e == 0xFF {
        return if m != 0 { NanF32 } else { frac }
    }
    if e == 0 {
        if m == 0 {
            return frac
        }
        s := raw.Clz64(u64(m)) - 40
        m = m << s
        e = 1 - s
    } else {
        m = m | 0x00800000
    }
    n := if exp > 1024 { 1024 } else if exp < -1024 { -1024 } else { exp }
    r := e + n
    if r >= 0xFF {
        return f32.FromBits(sign | ExpMask32)
    }
    if r >= 1 {
        return f32.FromBits(sign | Trunc[u32](r) << 23 | m & FracMask32)
    }
    s := 1 - r
    if s > 25 {
        return f32.FromBits(sign)
    }
    var q := m >> s
    rest := m & ((1 << s) - 1)
    half := Trunc[u32](1) << (s - 1)
    if rest > half || rest == half && q & 1 == 1 {
        q += 1
    }
    return f32.FromBits(sign | q)
}

/// NextAfterF32 is the f32 next to x in the direction of toward: x when
/// they are equal, Nan's f32 bits when either is a NaN. Exact. O(1);
/// allocates nothing.
export NextAfterF32(x: f32, toward: f32): f32 {
    if IsNanF32(x) || IsNanF32(toward) {
        return NanF32
    }
    if x == toward {
        return x
    }
    if x == 0.0 {
        return CopySignF32(f32.FromBits(1), toward)
    }
    b := x.Bits()
    if (toward > x) == (x > 0.0) {
        return f32.FromBits(b + 1)
    }
    return f32.FromBits(b - 1)
}

/// SqrtF32 is the correctly rounded square root: Nan's f32 bits below zero
/// or for a NaN, -0 for -0, inf for inf. O(1); allocates nothing.
export SqrtF32(x: f32): f32 {
    b := x.Bits()
    if b > ExpMask32 && b != SignBit32 {
        return NanF32 // a NaN or below zero, tested on the bits as in Sqrt
    }
    // The f64 root of an f32 rounds correctly to f32: 53 >= 2 * 24 + 2.
    return Cast[f32](raw.SqrtF64(f64(x)))
}

// Narrow rounds x to f32 once, to nearest with ties to even: an f64 at or
// past the f32 overflow threshold (the largest f32 plus half its ulp) is
// an infinity of x's sign, where Cast[f32] would panic, and a NaN is
// NanF32.
Narrow(x: f64): f32 {
    a := x.Bits() & AbsMask64
    if a > ExpMask64 {
        return NanF32
    }
    if a >= 0x47EFFFFFF0000000 {
        return f32.FromBits(if x.Bits() & SignBit64 != 0 { 0xFF800000 } else { 0x7F800000 })
    }
    return Cast[f32](x)
}

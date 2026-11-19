// Why: the smallest numeric helpers, and the float remainder codegen calls
// for a float %, belong in one place at the bottom of std.math.
//
// What: Min, Max, Abs, Sqrt, and Rem64 and Rem32, the float remainder that
// Mod and ModF32 return. The rest of std.math is in files named for what
// they hold (exact.mk, explog.mk, trig.mk, ...).
//
// How: every function is O(1) except Rem64 and Rem32, which take the exact
// truncated remainder by long division in the exponent difference, about
// (ex - ey) / 11 and / 40 integer divisions. Nothing allocates and nothing
// panics.

import std.raw

/// The lesser of a and b; a when neither is less (b.Less(&a) is false).
/// O(1); allocates nothing.
export Min[T: Ord + Plain](a: T, b: T): T {
    if b.Less(&a) {
        return b
    }
    return a
}

/// The greater of a and b; a when neither is greater (a.Less(&b) is
/// false). O(1); allocates nothing.
export Max[T: Ord + Plain](a: T, b: T): T {
    if a.Less(&b) {
        return b
    }
    return a
}

/// |x|: the sign bit cleared, so Abs(-0.0) is 0 and a NaN stays a NaN.
/// O(1); allocates nothing.
export Abs(x: f64): f64 {
    return f64.FromBits(x.Bits() & 0x7FFFFFFFFFFFFFFF)
}

/// The correctly rounded square root (IEEE 754 squareRoot): Nan below
/// zero and for a NaN, -0 for -0, inf for inf. O(1);
/// allocates nothing.
export Sqrt(x: f64): f64 {
    // The root is a NaN exactly when x is a NaN or below zero (IEEE 754
    // squareRoot; the root of -0 is -0), so the guard tests the root, not
    // x: its bits with the sign cleared above the infinity's are a NaN,
    // whatever NaN the hardware gave (x86_64's has the sign set), and it
    // becomes Nan. One and, one signed compare and one blend per root,
    // where the test of x cost five. It is tested
    // on the bits: with a float test LLVM may fold the guard into the
    // root, whose NaN is the hardware's.
    r := raw.SqrtF64(x)
    if r.Bits() & AbsMask64 > ExpMask64 {
        return Nan
    }
    return r
}

// Rem64 is a % b for f64: C's fmod, exact (the result is representable), with
// the sign of a; NaN when a is infinite, b is zero, or either is NaN; a when
// b is infinite and a finite. The NaN is the canonical one, as the
// evaluator's (internal/eval floatBinary, Go's math.Mod). Not exported: no
// program calls it by name, and codegen finds it by its key.
//
// Algorithm: integer mantissas, then r = mx mod my, and for the exponent
// difference r = (r << s) mod my with s <= 11, so r << s fits 64 bits
// (my < 2^53): one hardware division per 11 bits of difference, as glibc
// 2.38's and LLVM libc's fmod, where musl's takes one bit per step.
Rem64(a: f64, b: f64): f64 {
    ux := a.Bits()
    uy := b.Bits()
    sx := ux & 0x8000000000000000
    ax := ux & 0x7FFFFFFFFFFFFFFF
    ay := uy & 0x7FFFFFFFFFFFFFFF
    if ay == 0 || ax >= 0x7FF0000000000000 || ay > 0x7FF0000000000000 {
        return f64.FromBits(0x7FF8000000000000)
    }
    if ax < ay {
        return a // |a| < |b|, and b infinite with a finite
    }
    if ax == ay {
        return f64.FromBits(sx)
    }
    var ex := Trunc[int](ax >> 52)
    var ey := Trunc[int](ay >> 52)
    var mx := ax & 0xFFFFFFFFFFFFF
    var my := ay & 0xFFFFFFFFFFFFF
    if ex == 0 {
        ex = 1
    } else {
        mx = mx | 0x10000000000000
    }
    if ey == 0 {
        ey = 1
    } else {
        my = my | 0x10000000000000
    }
    var r := mx % my
    var d := ex - ey
    for d > 0 && r != 0 {
        s := if d > 11 { 11 } else { d }
        r = (r << s) % my
        d -= s
    }
    if r == 0 {
        return f64.FromBits(sx)
    }
    // r * 2^(ey - 1075): normalize to bit 52, or stay subnormal.
    lz := raw.Clz64(r) - 11
    if lz >= ey {
        return f64.FromBits(sx | (r << (ey - 1)))
    }
    return f64.FromBits(sx | ((r << lz) & 0xFFFFFFFFFFFFF) | (Trunc[u64](ey - lz) << 52))
}

// Rem32 is Rem64 for f32 (mantissas below 2^24, so 40 bits per division).
Rem32(a: f32, b: f32): f32 {
    ux := a.Bits()
    uy := b.Bits()
    sx := ux & 0x80000000
    ax := ux & 0x7FFFFFFF
    ay := uy & 0x7FFFFFFF
    if ay == 0 || ax >= 0x7F800000 || ay > 0x7F800000 {
        return f32.FromBits(0x7FC00000)
    }
    if ax < ay {
        return a
    }
    if ax == ay {
        return f32.FromBits(sx)
    }
    var ex := Trunc[int](ax >> 23)
    var ey := Trunc[int](ay >> 23)
    var mx := u64(ax & 0x7FFFFF)
    var my := u64(ay & 0x7FFFFF)
    if ex == 0 {
        ex = 1
    } else {
        mx = mx | 0x800000
    }
    if ey == 0 {
        ey = 1
    } else {
        my = my | 0x800000
    }
    var r := mx % my
    var d := ex - ey
    for d > 0 && r != 0 {
        s := if d > 40 { 40 } else { d }
        r = (r << s) % my
        d -= s
    }
    if r == 0 {
        return f32.FromBits(sx)
    }
    lz := raw.Clz64(r) - 40
    if lz >= ey {
        return f32.FromBits(sx | Trunc[u32](r << (ey - 1)))
    }
    return f32.FromBits(sx | Trunc[u32]((r << lz) & 0x7FFFFF) | (Trunc[u32](ey - lz) << 23))
}

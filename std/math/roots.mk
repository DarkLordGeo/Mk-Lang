// Why: Hypot and Cbrt must avoid overflow and stay within an ulp with the
// same bits everywhere.
//
// What: Hypot and Cbrt, with Cbrt's polynomial constants.
//
// How: Hypot is musl's hypot.c: the larger and smaller magnitude scaled
// into a safe range, each square taken exactly as a double-double by
// Dekker's split, the four parts summed smallest first, one square root.
// Cbrt is FreeBSD msun's s_cbrt.c: an estimate from the exponent divided by
// 3, a polynomial to 23 bits, and one Newton step to 53. Both are O(1),
// allocate nothing and never panic; a NaN result is Nan.

// Square is x * x as hi + lo exactly (Dekker, with Veltkamp's split).
Square(x: f64): (hi: f64, lo: f64) {
    c := 134217729.0 * x
    xh := x - c + c
    xl := x - xh
    hi := x * x
    return (hi: hi, lo: xh * xh - hi + 2.0 * xh * xl + xl * xl)
}

/// Hypot is sqrt(x^2 + y^2), without overflow or underflow on the way: inf when
/// either is infinite (a NaN in the other included), else Nan when either is a
/// NaN. Faithfully rounded (within 1 ulp) but not correctly rounded, so its
/// results may change in a minor release. TestSTDMATH_TrueError: largest error
/// 0.856 ulp over 21,681 inputs, 890 of them not correctly rounded. O(1);
/// allocates nothing.
export Hypot(x: f64, y: f64): f64 {
    var ux := x.Bits() & AbsMask64
    var uy := y.Bits() & AbsMask64
    if ux == ExpMask64 || uy == ExpMask64 {
        return Inf
    }
    if ux > ExpMask64 || uy > ExpMask64 {
        return Nan
    }
    if ux < uy {
        t := ux // swapped by hand: std.math is layer 0, std.core.Swap layer 1
        ux = uy
        uy = t
    }
    ex := Trunc[int](ux >> 52)
    ey := Trunc[int](uy >> 52)
    var a := f64.FromBits(ux)
    var b := f64.FromBits(uy)
    if uy == 0 {
        return a
    }
    if ex - ey > 64 {
        return a + b
    }
    var z := 1.0
    if ex > 0x3FF + 510 {
        z = f64.FromBits(0x6BB0000000000000) // 2^700
        a = a * f64.FromBits(0x1430000000000000)
        b = b * f64.FromBits(0x1430000000000000)
    } else if ey < 0x3FF - 450 {
        z = f64.FromBits(0x1430000000000000) // 2^-700
        a = a * f64.FromBits(0x6BB0000000000000)
        b = b * f64.FromBits(0x6BB0000000000000)
    }
    sa := Square(a)
    sb := Square(b)
    return z * Sqrt(sb.lo + sa.lo + sb.hi + sa.hi)
}

// s_cbrt.c's polynomial for the cube root's correction to 23 bits.
const CbrtP0: f64 = f64.FromBits(0x3FFE03E60F61E692) // 1.87595182427177009643
const CbrtP1: f64 = f64.FromBits(0xBFFE28E092F02420) // -1.88497979543377169875
const CbrtP2: f64 = f64.FromBits(0x3FF9F1604A49D6C2) // 1.621429720105354466140
const CbrtP3: f64 = f64.FromBits(0xBFE844CBBEE751D9) // -0.758397934778766047437
const CbrtP4: f64 = f64.FromBits(0x3FC2B000D4E4EDD7) // 0.145996192886612446982

/// Cbrt is the real cube root of x, with x's sign: a zero or an infinity is
/// itself, and a NaN gives Nan. Faithfully rounded (within 1 ulp) but not
/// correctly rounded, so its results may change in a minor release.
/// TestSTDMATH_TrueError: largest error 0.664 ulp over 20,187 inputs, 1,641 of
/// them not correctly rounded. O(1); allocates nothing.
export Cbrt(x: f64): f64 {
    b := x.Bits()
    sign := b & SignBit64
    a := b & AbsMask64
    if a >= ExpMask64 {
        return if a > ExpMask64 { Nan } else { x }
    }
    if a == 0 {
        return x
    }
    // The estimate: the high word over 3, plus a bias that centres the
    // error (B1 = (1023 - 1023/3 - 0.03306235651) * 2^20; B2 = B1 - 18 * 2^20
    // after scaling a subnormal by 2^54).
    var t := 0.0
    if a < 0x0010000000000000 {
        hs := ((x * f64.FromBits(0x4350000000000000)).Bits() & AbsMask64) >> 32
        t = f64.FromBits(sign | (hs / 3 + 696219795) << 32)
    } else {
        t = f64.FromBits(sign | ((a >> 32) / 3 + 715094163) << 32)
    }
    // A polynomial in r = t^3 / x refines t to about 23 bits.
    var r := (t * t) * (t / x)
    t = t * ((CbrtP0 + r * (CbrtP1 + r * CbrtP2)) + ((r * r) * r) * (CbrtP3 + r * CbrtP4))
    // Rounded away from zero to 23 bits, so t * t below is exact and t is
    // at least the cube root.
    t = f64.FromBits((t.Bits() + 0x80000000) & 0xFFFFFFFFC0000000)
    // One Newton step to 53 bits.
    s := t * t
    r = x / s
    w := t + t
    r = (r - t) / (w + r)
    return t + t * r
}

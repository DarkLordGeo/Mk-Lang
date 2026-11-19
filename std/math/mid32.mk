// Why: rounding a faithfully rounded f64 result to f32 can round twice the
// wrong way at an f32 midpoint; the two-argument F32 functions must be
// correctly rounded anyway.
//
// What: Atan2F32, HypotF32 and PowF32.
//
// How: each widens its arguments to f64 (exact), takes an f64 result r one
// of the two f64s around the true value T, and rounds r once to f32 with
// Narrow. A midpoint between two f32s is itself an f64, so r and T round
// alike unless r is a midpoint and T is not; only then does a function look
// further and decide which side T lies: HypotF32 exactly, PowF32 and
// Atan2F32 by a more precise evaluation. All O(1), no allocation, no panic;
// a NaN result is NanF32.

import std.raw

// 2^150: an f64 below 2^-126 times Two150 is exact and counts f32
// subnormal half-steps (Mid32).
const Two150: f64 = f64.FromBits(0x4950000000000000)

// Mid32 says whether y is a midpoint between two adjacent f32s (or between
// 0 and the least subnormal), where rounding y to f32 is a tie. From
// 2^-126 to 2^128 an f32 keeps the top 23 of the 52 fraction bits, so a
// midpoint has 0x10000000 in the 29 bits below (the overflow threshold,
// the largest f32 plus half its ulp, included); below 2^-126 the f32 grid
// is 2^-149, so a midpoint is an odd multiple of 2^-150.
Mid32(y: f64): bool {
    a := y.Bits() & AbsMask64
    if a >= 0x3810000000000000 {
        return a < 0x47F0000000000000 && a & 0x1FFFFFFF == 0x10000000
    }
    q := f64.FromBits(a) * Two150
    if q < 1.0 || Floor(q) != q {
        return false
    }
    return Cast[int](q) & 1 == 1
}

// Away is m moved one f64 ulp away from zero when up, else one toward it:
// for a midpoint m, a value Narrow rounds as it rounds the true value on
// that side.
Away(m: f64, up: bool): f64 {
    return f64.FromBits(if up { m.Bits() + 1 } else { m.Bits() - 1 })
}

/// HypotF32 is Hypot for f32: inf when either is infinite (a NaN in the other
/// included), else Nan's f32 bits when either is a NaN. Correctly rounded for
/// every input. O(1); allocates nothing.
export HypotF32(x: f32, y: f32): f32 {
    a := f64(x)
    b := f64(y)
    if !IsFinite(a) || !IsFinite(b) {
        return Narrow(Hypot(a, b))
    }
    // a * a and b * b have at most 48 significant bits and lie in
    // [2^-298, 2^256]: exact. s.hi + s.lo is their sum exactly.
    s := TwoSum(a * a, b * b)
    // |s.lo| <= ulp(s.hi) / 2 moves the square root by at most 0.36 ulp
    // of r, and r is within 0.5 ulp of sqrt(s.hi): r is faithful.
    r := raw.SqrtF64(s.hi)
    if !Mid32(r) {
        return Narrow(r)
    }
    // r = m has at most 25 significant bits, so r * r is exact, and s.hi -
    // r * r is exact (Sterbenz); the rounded sum with s.lo keeps the sign
    // of the exact x^2 + y^2 - m^2, and is 0 only when T = m.
    d := (s.hi - r * r) + s.lo
    if d == 0.0 {
        return Narrow(r)
    }
    return Narrow(Away(r, d > 0.0))
}

/// Atan2F32 is Atan2 for f32. Largest error 0.500 ulp over the 21,369 inputs of
/// TestSTDMATH_Oracle and the 80 of TestSTDMATH_Midpoint, every one correctly
/// rounded; not proved correctly rounded for every input, so its results may
/// change in a minor release. None of the 238 near-midpoint inputs of
/// TestSTDMATH_Atan2Search is misrounded. O(1); allocates nothing.
export Atan2F32(y: f32, x: f32): f32 {
    yd := f64(y)
    xd := f64(x)
    r := Atan2(yd, xd)
    if !Mid32(r) {
        return Narrow(r)
    }
    t := Atan2Tail(yd, xd)
    if t == 0.0 {
        return Narrow(r)
    }
    return Narrow(Away(r, t > 0.0))
}

// Atan2Tail is a value whose sign says which side of |Atan2(y, x)| the
// true |atan2(y, x)| lies, for an Atan2 result on an f32 midpoint, which
// only Atan2's general case and its y / x case produce (the other cases
// return 0, +-pi/4, +-3pi/4, +-pi/2 or +-pi, none a midpoint).
Atan2Tail(y: f64, x: f64): f64 {
    ax := f64.FromBits(x.Bits() & AbsMask64)
    ay := f64.FromBits(y.Bits() & AbsMask64)
    fx := FrExp(ax)
    fy := FrExp(ay)
    k := fy.exp - fx.exp
    if k < -60 {
        // Atan2 returned y / x, and atan q lies below q by about q^3 / 3
        // (2^-118 relative), far less than any nonzero remainder of y / x.
        // For f32 arguments a quotient on a midpoint is exact (a nonzero
        // remainder is at least 2^-49 of it), so the remainder is 0 there.
        q := ay / ax
        p := TwoProd(q, ax)
        // ay - p.hi is exact (Sterbenz); the rounded difference keeps the
        // sign of the exact remainder.
        rem := (ay - p.hi) - p.lo
        return if rem != 0.0 { rem } else { -1.0 }
    }
    return Atan2Core(LdExp(fy.frac, k), fx.frac, x.Bits() & SignBit64 != 0).lo
}

/// PowF32 is Pow for f32, with Pow's special cases. Largest error 0.500 ulp
/// over the 21,369 inputs of TestSTDMATH_Oracle and the 94 of
/// TestSTDMATH_Midpoint, every one correctly rounded; not proved correctly
/// rounded for every input, so its results may change in a minor release. None
/// of the 45,816 near-midpoint inputs of TestSTDMATH_PowSearch is misrounded,
/// an exact power included. O(1); allocates nothing.
export PowF32(x: f32, y: f32): f32 {
    xd := f64(x)
    yd := f64(y)
    r := Pow(xd, yd)
    if !Mid32(r) {
        return Narrow(r)
    }
    return Narrow(PowMid(xd, yd, r))
}

// PowMidTol is 2^-90, above PowMid's evaluation error of d (about 2^-96:
// LnPrecise's 2^-98 at |ln| near 104, and |y ln x| <= 104 for any f32
// midpoint result).
const PowMidTol: f64 = f64.FromBits(0x3A50000000000000)

// PowMid is m, an f32 midpoint Pow(x, y) rounded to, when x^y = m, and
// otherwise m moved one f64 ulp toward x^y. x and y are finite f32 values
// (x not +-1 or 0, y not 0); |x|^y and |m| are compared through d = y
// ln|x| - ln|m|.
PowMid(x: f64, y: f64, m: f64): f64 {
    ax := f64.FromBits(x.Bits() & AbsMask64)
    am := f64.FromBits(m.Bits() & AbsMask64)
    lx := LnPrecise(ax)
    lm := LnPrecise(am)
    // y lx.hi is exact as p.hi + p.lo, and p.hi - lm.hi as s.hi + s.lo.
    p := TwoProd(y, lx.hi)
    s := TwoSum(p.hi, -lm.hi)
    d := s.hi + (s.lo + ((p.lo + y * lx.lo) - lm.lo))
    if d.Bits() & AbsMask64 <= PowMidTol.Bits() {
        // Too close to call by d: an exact power is a tie, which Narrow
        // breaks to even; otherwise d's sign is the best estimate left.
        if d == 0.0 || PowExact(ax, y, am) {
            return m
        }
    }
    return Away(m, d > 0.0)
}

// OddPart is v = odd 2^exp with odd an odd integer, for a positive normal
// v.
OddPart(v: f64): (odd: u64, exp: int) {
    b := v.Bits()
    mant := (b & FracMask64) | 0x0010000000000000
    tz := raw.Ctz64(mant)
    return (odd: mant >> tz, exp: Trunc[int](b >> 52) - 1075 + tz)
}

// PowExact says whether x^y = m exactly, for positive normal x and m with
// at most 25 significant bits and a finite y != 0. With x = ox 2^ex, m =
// om 2^em and y = Y 2^-k (ox, om and Y odd): if ox or om is 1, both must
// be, and then x^y = 2^(ex y) = m when ex y = em. Otherwise y > 0 (a
// product of odd powers is 1 only when each base is), and x^y = m needs
// ox^Y = om^(2^k), so ox = t^(2^k) and om = t^Y for an integer t >= 3:
// t^(2^k) < 2^25 gives k <= 3 and t^Y < 2^25 gives Y 2^-k <= 15.
PowExact(x: f64, y: f64, m: f64): bool {
    a := OddPart(x)
    b := OddPart(m)
    exps := Cast[f64](a.exp) * y == Cast[f64](b.exp)
    if a.odd == 1 || b.odd == 1 {
        return a.odd == b.odd && exps
    }
    if y < 0.0 || !exps {
        return false
    }
    c := OddPart(y)
    var t := Trunc[int](a.odd)
    var n := Trunc[int](c.odd)
    if c.exp < 0 {
        k := -c.exp
        if k > 3 {
            return false
        }
        // t = ox^(2^-k), when ox is a 2^k-th power.
        for _ in 0..k {
            s := Cast[int](raw.SqrtF64(Cast[f64](t)))
            if s * s != t {
                return false
            }
            t = s
        }
    } else {
        if c.exp > 3 {
            return false
        }
        n = n << c.exp
    }
    if n > 15 {
        return false
    }
    om := Trunc[int](b.odd)
    var p := 1
    for _ in 0..n {
        p = p * t
        if p > om {
            return false
        }
    }
    return p == om
}

// The coefficients (-1)^(n+1) / n of log1p's series for n = 8 .. 16, the
// part LnPrecise sums in f64.
const Ln1pC8: f64 = -1.0 / 8.0
const Ln1pC9: f64 = 1.0 / 9.0
const Ln1pC10: f64 = -1.0 / 10.0
const Ln1pC11: f64 = 1.0 / 11.0
const Ln1pC12: f64 = -1.0 / 12.0
const Ln1pC13: f64 = 1.0 / 13.0
const Ln1pC14: f64 = -1.0 / 14.0
const Ln1pC15: f64 = 1.0 / 15.0
const Ln1pC16: f64 = -1.0 / 16.0

// DDMul is (ah + al)(bh + bl) as hi + lo, to about 2^-104 relative, for
// values whose product lies in TwoProd's range.
DDMul(ah: f64, al: f64, bh: f64, bl: f64): (hi: f64, lo: f64) {
    p := TwoProd(ah, bh)
    return FastTwoSum(p.hi, p.lo + (ah * bl + al * bh))
}

// DDDivN is (h + l) / n as hi + lo, to about 2^-104 relative, for a small
// integer n: q = h / n and its remainder h - q n, exact by TwoProd and
// Sterbenz.
DDDivN(h: f64, l: f64, n: f64): (hi: f64, lo: f64) {
    q := h / n
    p := TwoProd(q, n)
    return FastTwoSum(q, (((h - p.hi) - p.lo) + l) / n)
}

// LnPrecise is ln v as hi + lo for a positive normal v in [2^-151, 2^128],
// with an absolute error below 2^-98, and below 2^-100 |ln v| for v within
// 1/128 of 1. v = 2^k mm with mm in [sqrt2/2, sqrt2), c from LogTable's row
// nearest mm (dd.mk), r = mm c - 1 exactly as rh + rl by TwoProd (|r| <=
// 0.0112 < 2^-6.4); ln v = k ln2 - ln c + log1p(r), with k ln2 from
// ExpDD's three parts of ln2 / 32 (the first two exact for |k| < 2^11),
// -ln c from LogTable as a double-double, and log1p(r) by its series: the
// terms to r^7 as double-doubles, r^8 to r^16 in f64 (each below 2^-51
// |r|, so their rounding is below 2^-104 |r|), truncated below |r|^17 / 17
// < 2^-112.
LnPrecise(v: f64): (hi: f64, lo: f64) {
    b := v.Bits()
    var k := Trunc[int](b >> 52) - 1023
    var mb := (b & FracMask64) | 0x3FF0000000000000
    if mb > 0x3FF6A09E667F3BCD {
        mb = mb - 0x0010000000000000
        k = k + 1
    }
    mm := f64.FromBits(mb)
    row := 3 * (Cast[int](mm * 64.0 + 0.5) - 45)
    pc := TwoProd(mm, f64.FromBits(LogTable[row]))
    // pc.hi - 1 is exact (Sterbenz).
    r := TwoSum(pc.hi - 1.0, pc.lo)
    var ph := r.hi
    var pl := r.lo
    var sh := r.hi
    var sl := r.lo
    for n in 2..8 {
        q := DDMul(ph, pl, r.hi, r.lo)
        ph = q.hi
        pl = q.lo
        t := DDDivN(ph, pl, Cast[f64](n))
        var th := t.hi
        var tl := t.lo
        if n & 1 == 0 {
            th = -th
            tl = -tl
        }
        u := TwoSum(sh, th)
        w := FastTwoSum(u.hi, u.lo + (sl + tl))
        sh = w.hi
        sl = w.lo
    }
    rh := r.hi
    tail := ph * rh * (Ln1pC8 + rh * (Ln1pC9 + rh * (Ln1pC10 + rh * (Ln1pC11 + rh * (Ln1pC12 + rh * (Ln1pC13 + rh * (Ln1pC14 + rh * (Ln1pC15 + rh * Ln1pC16))))))))
    l1 := FastTwoSum(sh, sl + tail)
    // k 32 ExpL1 and k 32 ExpL2 are exact: 37-bit constants, |k| < 2^11.
    kf := Cast[f64](k) * 32.0
    a := TwoSum(kf * ExpL1, f64.FromBits(LogTable[row + 1]))
    c := TwoSum(a.hi, kf * ExpL2)
    d := TwoSum(c.hi, l1.hi)
    return FastTwoSum(d.hi, a.lo + c.lo + d.lo + ((kf * ExpL3 + f64.FromBits(LogTable[row + 2])) + l1.lo))
}

// Why: x * y + z rounded once must give the same bits with or without a
// hardware FMA, and in the evaluator.
//
// What: Fma and FmaF32, with Fma's two paths FmaHw (the instruction) and
// FmaSoft (software, with its general path FmaInt).
//
// How: Fma is the fused multiply-add instruction where the running CPU has
// it (simd.Cpu()), the one cpu dispatch of std.math: IEEE 754 defines that
// result bit for bit, so the software form, which runs at the baseline and
// in the evaluator, gives the same bits. FmaInt follows Go's math.FMA: the
// 106-bit product in two words through the 128-bit multiply, z aligned with
// a sticky bit, one add or subtract, then one rounding to nearest even; for
// operands of ordinary size, FmaSoft first takes a branch-free path in f64
// arithmetic that rounds to odd, correctly rounded too. FmaF32 takes the
// f32 product exactly in f64 (48 bits), adds z with Knuth's TwoSum to get
// the error of that add, rounds the f64 sum to odd, then rounds once to
// f32, which is correct since 53 >= 24 + 2 (Boldo and Melquiond,
// "Emulation of FMA and correctly rounded sums: proved algorithms using
// rounding to odd", 2008). Nothing allocates and nothing panics.

import std.raw
import std.simd

// Fields of an f64 split for the software FMA: the sign bit, the biased
// exponent (below 1 for a subnormal, normalized), the 53-bit mantissa
// with its leading one at bit 52.
struct Parts {
    sign: u64
    exp: int
    mant: u64
}

SplitParts(b: u64): Parts {
    e := Trunc[int]((b >> 52) & 0x7FF)
    m := b & FracMask64
    if e == 0 {
        s := raw.Clz64(m) - 11
        return Parts(sign: b >> 63, exp: 1 - s, mant: m << s)
    }
    return Parts(sign: b >> 63, exp: e, mant: m | 0x0010000000000000)
}

// ShlWide shifts the 128-bit hi:lo left by n, 0 <= n < 128.
ShlWide(hi: u64, lo: u64, n: int): (hi: u64, lo: u64) {
    if n == 0 {
        return (hi: hi, lo: lo)
    }
    if n >= 64 {
        return (hi: lo << (n - 64), lo: 0)
    }
    return (hi: hi << n | lo >> (64 - n), lo: lo << n)
}

// ShrSticky shifts the 128-bit hi:lo right by n >= 0 and ors every bit
// shifted out into bit 0, so rounding still sees that the value was above
// the truncation.
ShrSticky(hi: u64, lo: u64, n: int): (hi: u64, lo: u64) {
    if n == 0 {
        return (hi: hi, lo: lo)
    }
    if n >= 128 {
        return (hi: 0, lo: if hi | lo != 0 { 1 } else { 0 })
    }
    if n >= 64 {
        k := n - 64
        lost := hi & ((Trunc[u64](1) << k) - 1) | lo
        return (hi: 0, lo: hi >> k | Sticky(lost))
    }
    lost := lo & ((Trunc[u64](1) << n) - 1)
    return (hi: hi >> n, lo: lo >> n | hi << (64 - n) | Sticky(lost))
}

/// Fma is x * y + z rounded once, to nearest with ties to even: correctly
/// rounded, with Nan for a NaN result. On a CPU with the fused multiply-add
/// instruction (simd.Cpu()) it is the instruction; elsewhere, and in the
/// evaluator, a software routine. IEEE 754 defines the instruction's result
/// bit for bit, so the result is the same either way, on every target and
/// at compile time. O(1); allocates nothing.
export Fma(x: f64, y: f64, z: f64): f64 {
    if simd.Cpu().Has("fma") {
        var r := 0.0
        unsafe "Cpu() reports fma, which FmaHw's clause names" {
            r = FmaHw(x, y, z)
        }
        return r
    }
    return FmaSoft(x, y, z)
}

// FmaHw is Fma by the instruction: one lane of Vec.Fma, which LLVM's fma
// lowers to vfmadd (a scalar one once the splats fold), with a NaN result
// made Nan's bits as the software forms make it; the instruction's
// own NaN keeps an input's payload, or is the default NaN with its sign
// bit set. tests_fma.mk runs it against FmaInt where the CPU has fma.
FmaHw(x: f64, y: f64, z: f64): f64 cpu "fma" {
    r := simd.Vec[f64, 2].Splat(x).Fma(simd.Vec[f64, 2].Splat(y), simd.Vec[f64, 2].Splat(z)).Lane(0)
    return if IsNan(r) { Nan } else { r } // tested on the bits, as Sqrt
}

// FmaSoft is Fma in software, the reference both paths are held to: the
// evaluator's path (its Cpu() is the target's baseline, which has no fma)
// and the path of a CPU without the instruction.
FmaSoft(x: f64, y: f64, z: f64): f64 {
    ex := (x.Bits() >> 52) & 0x7FF
    ey := (y.Bits() >> 52) & 0x7FF
    ez := (z.Bits() >> 52) & 0x7FF
    // The fast path, for biased exponents of x and y in [768, 1279] (2^-255
    // <= |x|, |y| < 2^257: both normal) and of z below 2023 (|z| < 2^1000:
    // zero and subnormal included). Boldo and Melquiond's emulation by
    // rounding to odd (2008, as glibc's dbl-64 s_fma.c), correctly rounded
    // as FmaInt is, so the same bits. The steps, and why each is exact
    // where it must be:
    //   - x y = ph + pl exactly (TwoProd): 134217729 x and y stay below
    //     2^285, and every partial product is a multiple of ulp(x) ulp(y) >=
    //     2^-614, far above the subnormal grid, so Dekker's split and sums
    //     are exact (TwoProd needs |x|, |y| < 2^995 and |x y| >= 2^-969).
    //   - z + ph = th + tl exactly (TwoSum, exact for any finite operands,
    //     subnormal ones included, whose sum does not overflow: |z| + |ph| <
    //     2^1001).
    //   - v = tl + pl rounded to odd: s = tl + pl rounded to nearest and its
    //     exact error e (TwoSum again); if e != 0 and s's last bit is even,
    //     s's neighbour on e's side, whose last bit is odd: one up on the
    //     bits away from zero, one down towards it (down from a power of
    //     two is the largest f64 of the binade below, odd too). s = 0 only
    //     when tl + pl = 0, so e = 0 there.
    //   - th + v rounded to nearest is x y + z rounded once. If v is exact,
    //     th + v is x y + z itself. Else e != 0, so tl != 0 and z + ph was
    //     inexact: then |th| >= |ph| / 2 (Sterbenz makes a nearer
    //     cancellation exact), so th is normal; with u = ulp(th), |tl| <= u
    //     / 2 and |pl| <= ulp(ph) / 2 <= u, so |tl + pl| <= 1.5 u and v is an
    //     odd multiple of some g <= 2^-52 u with tl + pl strictly within g
    //     of v. th is a multiple of 2 g, so x y + z = th + tl + pl and th +
    //     v lie strictly between the same two even multiples of g; and |x y
    //     + z| >= |th| - 1.5 u > 2^51 u, where every f64 and every midpoint
    //     between two is a multiple of u / 4, so of 2 g. No rounding
    //     boundary lies between th + v and x y + z, so they round alike
    //     (rounding to odd with two spare bits).
    //   - Zeros: an exact cancellation (z = -ph, pl = 0, as ph + pl itself
    //     is never an f64 when pl != 0) gives th = tl = +0, v = +0 and +0,
    //     IEEE's sum; z = +-0 gives th = ph, tl = +0, v = pl and ph + pl
    //     rounded, ph = x * y, nonzero here, as FmaInt's x * y.
    // A nonzero x y + z is at least 2^-614 in magnitude (a multiple of it
    // when z cancels x y, else above |x y| / 2 >= 2^-511) and below 2^1001,
    // so no result is subnormal or infinite. The window is 512 exponents
    // wide so that one or of the two offsets tests both; the true limits
    // above (Dekker's, and no overflow) are far outside it.
    if ((ex -% 768) | (ey -% 768)) < 512 && ez < 2023 {
        p := TwoProd(x, y)
        t := TwoSum(z, p.hi)
        r := TwoSum(t.lo, p.lo)
        // On the bits, without a branch (s's parity is a coin flip): with
        // inexact 1 when e != 0 and down 1 when e's sign is not s's too,
        // s - down, then or 1, is s | 1 away from zero and (s - 1) | 1
        // towards it, the odd one of s and that neighbour. Neither step
        // wraps, as s != 0 when e != 0.
        sb := r.hi.Bits()
        inexact: u64 = if r.lo != 0.0 { 1 } else { 0 }
        down := inexact & ((r.lo.Bits() ^ sb) >> 63)
        return t.hi + f64.FromBits((sb -% down) | inexact)
    }
    return FmaInt(x, y, z)
}

// FmaInt is FmaSoft for every input, by Go's integer method (the file
// comment): the reference FmaSoft's fast path and FmaHw are tested against
// (tests_fma.mk).
FmaInt(x: f64, y: f64, z: f64): f64 {
    bx := x.Bits()
    by := y.Bits()
    bz := z.Bits()
    // A zero or a non-finite factor: the product is exact, so one rounding.
    if x == 0.0 || y == 0.0 || bx & ExpMask64 == ExpMask64 || by & ExpMask64 == ExpMask64 {
        r := x * y + z
        return if IsNan(r) { Nan } else { r } // tested on the bits, as Sqrt
    }
    // A zero z keeps the product's sign, which x * y + 0.0 could lose.
    if z == 0.0 {
        return x * y
    }
    if bz & ExpMask64 == ExpMask64 {
        return if IsNan(z) { Nan } else { z }
    }
    px := SplitParts(bx)
    py := SplitParts(by)
    pz := SplitParts(bz)
    var ps := px.sign ^ py.sign
    var pe := px.exp + py.exp - 1023 + 1
    // The product with its top bit at 125 or 126; normalized to 126.
    p := raw.Mul128(px.mant << 10, py.mant << 11)
    var ph := p.hi
    var pl := p.lo
    if ph >> 62 == 0 {
        n := ShlWide(ph, pl, 1)
        ph = n.hi
        pl = n.lo
        pe -= 1
    }
    var zs := pz.sign
    var ze := pz.exp
    var zh := pz.mant << 10
    var zl: u64 = 0
    // Order the operands so that |p| >= |z|.
    if pe < ze || pe == ze && ph < zh {
        // Swapped by hand: std.math is layer 0 and std.core.Swap is layer 1.
        ts := ps
        ps = zs
        zs = ts
        te := pe
        pe = ze
        ze = te
        th := ph
        ph = zh
        zh = th
        tl := pl
        pl = zl
        zl = tl
    }
    if ps != zs && pe == ze && ph == zh && pl == zl {
        return 0.0 // an exact cancellation is +0 in round to nearest
    }
    za := ShrSticky(zh, zl, pe - ze)
    var m: u64 = 0
    if ps == zs {
        lo := pl +% za.lo
        carry: u64 = if lo < pl { 1 } else { 0 }
        hi := ph +% za.hi +% carry
        if hi >> 63 == 0 {
            pe -= 1
        }
        m = ShrSticky(hi, lo, 64 + Trunc[int](hi >> 63)).lo
    } else {
        lo := pl -% za.lo
        borrow: u64 = if pl < za.lo { 1 } else { 0 }
        hi := ph -% za.hi -% borrow
        nz := if hi == 0 { 64 + raw.Clz64(lo) } else { raw.Clz64(hi) }
        pe -= nz
        n := ShlWide(hi, lo, nz - 1)
        m = n.hi | Sticky(n.lo)
    }
    // m has its leading one at bit 62 and ten bits below the result's last.
    if pe > 2045 || pe == 2045 && (m + 0x200) >> 63 == 1 {
        return f64.FromBits(ps << 63 | ExpMask64)
    }
    if pe < 0 {
        n := -pe
        m = if n >= 63 { 1 } else { m >> n | Sticky(m & ((Trunc[u64](1) << n) - 1)) }
        pe = 0
    }
    tie := m & 0x3FF == 0x200
    m = (m + 0x200) >> 10
    if tie {
        m = m & ~Trunc[u64](1)
    }
    if m == 0 {
        pe = 0
    }
    // m's bit 52, when set, adds one to the exponent field, as intended.
    return f64.FromBits((ps << 63) + (Trunc[u64](pe) << 52) + m)
}

/// FmaF32 is x * y + z rounded once to f32, to nearest with ties to even:
/// correctly rounded, with Nan's f32 bits for a NaN result. O(1); allocates
/// nothing.
export FmaF32(x: f32, y: f32, z: f32): f32 {
    xy := f64(x) * f64(y) // exact: 48 bits, and in f64's normal range
    zd := f64(z)
    s := xy + zd
    if !IsFinite(s) || s == 0.0 {
        return Narrow(s) // exact cases: an infinity, a NaN, a cancellation
    }
    // TwoSum: s + t is xy + zd exactly.
    v := s - xy
    t := (xy - (s - v)) + (zd - v)
    var b := s.Bits()
    if t != 0.0 && b & 1 == 0 {
        // Round to odd: step to the neighbour on t's side, whose last bit is 1.
        if (t > 0.0) == (s > 0.0) {
            b += 1
        } else {
            b -= 1
        }
    }
    return Narrow(f64.FromBits(b))
}

// Sticky is 1 when any bit of lost is set, else 0: the bit a right shift
// keeps of what it dropped.
Sticky(lost: u64): u64 {
    return if lost != 0 { 1 } else { 0 }
}

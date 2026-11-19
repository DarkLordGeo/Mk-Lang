// Why: Atan2's fast path (atrig.mk) and AtanDD's branch for th < 0.4375
// (dd.mk) must give the bits of the general form, which they skip.
// What: test blocks comparing each with the general form, written out here.
// How: random inputs and the edges: exponents just inside and just outside the
// fast path's window [-511, 512], exponent differences of 59 to 61, quotients
// beside atan's reduction boundaries, and zero tails of either sign.

// AtMix is splitmix64's finalizer.
AtMix(z0: u64): u64 {
    var z := z0
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

// AtF64 is an f64 with z's mantissa, the unbiased exponent e and z's bit
// 52 as its sign.
AtF64(z: u64, e: int): f64 {
    return f64.FromBits((z & 0x0010000000000000) << 11 | Cast[u64](e + 1023) << 52 | (z & 0x000FFFFFFFFFFFFF))
}

// Atan2General is Atan2's general path for finite, nonzero y and x: k from
// FrExp, then Atan2Core on y and x scaled by FrExp's power of x.
Atan2General(y: f64, x: f64): f64 {
    ynegative := y.Bits() & SignBit64 != 0
    xnegative := x.Bits() & SignBit64 != 0
    fx := FrExp(Abs(x))
    fy := FrExp(Abs(y))
    k := fy.exp - fx.exp
    if k > 60 {
        return if ynegative { -Pio2Hi } else { Pio2Hi }
    }
    if k < -60 {
        if !xnegative {
            return y / x
        }
        return if ynegative { -PiHi } else { PiHi }
    }
    z := Atan2Core(LdExp(fy.frac, k), fx.frac, xnegative).hi
    return if ynegative { -z } else { z }
}

// AtanDDGeneral is AtanDD's general path with id = -1: the quotient by
// dh = 1.0 and its remainder.
AtanDDGeneral(th: f64, tl: f64): (hi: f64, lo: f64) {
    dh := 1.0
    dl := 0.0
    zh := th / dh
    p := TwoProd(zh, dh)
    zl := (((th - p.hi) - p.lo) + tl - zh * dl) / dh
    z := zh * zh
    w := z * z
    s1 := z * (AT0 + w * (AT2 + w * (AT4 + w * (AT6 + w * (AT8 + w * AT10)))))
    s2 := w * (AT1 + w * (AT3 + w * (AT5 + w * (AT7 + w * AT9))))
    poly := zh * (s1 + s2)
    corr := zl / (1.0 + z)
    a := FastTwoSum(zh, -poly)
    return FastTwoSum(a.hi, a.lo + corr)
}

test "Atan2's fast path gives the general path's bits" {
    // The window is [-511, 512]: -512 and 513 lie just outside.
    exps: [10]int = [-514, -513, -512, -511, -510, 510, 511, 512, 513, 514]
    var st: u64 = 0x3C6EF372FE94F82B
    for i in 0..10 {
        for j in 0..10 {
            for _ in 0..6 {
                st = st +% 0x9E3779B97F4A7C15
                a := AtMix(st)
                st = st +% 0x9E3779B97F4A7C15
                b := AtMix(st)
                y := AtF64(a, exps[i])
                x := AtF64(b, exps[j])
                Assert(Atan2(y, x).Bits() == Atan2General(y, x).Bits())
            }
        }
    }
    // Exponent differences around the cut at 60, at the window's edges and
    // inside it.
    bases: [5]int = [-511, -450, -3, 451, 512]
    for i in 0..5 {
        for d in -62..63 {
            for _ in 0..2 {
                st = st +% 0x9E3779B97F4A7C15
                a := AtMix(st)
                st = st +% 0x9E3779B97F4A7C15
                b := AtMix(st)
                e := bases[i]
                ey := if e > 0 { e - 62 + d } else { e + 62 + d }
                y := AtF64(a, ey)
                x := AtF64(b, e)
                Assert(Atan2(y, x).Bits() == Atan2General(y, x).Bits())
                Assert(Atan2(x, y).Bits() == Atan2General(x, y).Bits())
            }
        }
    }
    // Random exponents in [-12, 19], and quotients beside atan's
    // reduction boundaries 7/16, 11/16, 19/16 and 39/16.
    cs: [4]f64 = [0.4375, 0.6875, 1.1875, 2.4375]
    for i in 0..20_000 {
        st = st +% 0x9E3779B97F4A7C15
        a := AtMix(st)
        st = st +% 0x9E3779B97F4A7C15
        b := AtMix(st)
        x := AtF64(a, Trunc[int](a >> 59) - 12)
        var y := AtF64(b, Trunc[int](b >> 59) - 12)
        if i % 2 == 1 {
            y = f64.FromBits((x * cs[Trunc[int](b & 3)]).Bits() +% ((b >> 8) & 15) -% 8)
        }
        Assert(Atan2(y, x).Bits() == Atan2General(y, x).Bits())
    }
}

test "AtanDD's branch below 0.4375 gives the general path's bits" {
    var st: u64 = 0xA54FF53A5F1D36F1
    for i in 0..20_000 {
        st = st +% 0x9E3779B97F4A7C15
        a := AtMix(st)
        st = st +% 0x9E3779B97F4A7C15
        b := AtMix(st)
        // th in [2^-62, 0.4375), tl within an ulp of th, or a zero of
        // either sign.
        var th := f64.FromBits(a & 0x000FFFFFFFFFFFFF | Cast[u64](1023 - 62 + Trunc[int](a >> 58) % 61) << 52)
        if th >= 0.4375 {
            th = th * 0.5
        }
        var tl := (Cast[f64](Cast[i64](b >> 11)) * 0.00000000000000011102230246251565) * (th * 0.00000000000000011102230246251565)
        if b & 1 == 1 {
            tl = -tl
        }
        if i % 7 == 0 {
            tl = if b & 2 == 0 { 0.0 } else { -0.0 }
        }
        r := AtanDD(th, tl)
        g := AtanDDGeneral(th, tl)
        Assert(r.hi.Bits() == g.hi.Bits() && r.lo.Bits() == g.lo.Bits())
    }
}

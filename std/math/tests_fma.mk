// Why: Fma dispatches at run time to FmaHw (the instruction) or FmaSoft, and
// both must give the bits of FmaInt, the integer path, on every input.
// What: test blocks for FmaSoft (its fast path and, past it, FmaInt), FmaHw and
// Fma's dispatch, each called directly, and for NaN results on both paths.
// How: a fixed-seed sample of each input kind (random, cancelling, short
// mantissas, the window's edges, tiny products beside a subnormal z, wide
// range, one operand dominant, built ties and near-ties at z's precision) and
// the exact edges of the fast path's exponent window. The evaluator's Fma is
// FmaSoft (its Cpu() is the baseline); TestSTDMATH_SameBitsEverywhere holds
// it to the native one's bits.

import std.simd

// FmaMix is splitmix64's finalizer.
FmaMix(z0: u64): u64 {
    var z := z0
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

// FmaF64 is an f64 with z's mantissa, an unbiased exponent in [lo, hi]
// from z's top bits and z's bit 52 as its sign.
FmaF64(z: u64, lo: int, hi: int): f64 {
    span := Cast[u64](hi - lo + 1)
    e := Cast[u64](lo + 1023) + (z >> 53) % span
    sign := (z & 0x0010000000000000) << 11
    return f64.FromBits(sign | (e << 52) | (z & 0x000FFFFFFFFFFFFF))
}

// FmaPow2 is 2^e for a normal exponent e.
FmaPow2(e: int): f64 {
    return f64.FromBits(Cast[u64](1023 + e) << 52)
}

// FmaNearTie is x, y and z with x y = 1 - j^2 2^-54 (or x y = 1 + d with
// |d| near 2^-53, y the rounded 1 / x), both scaled by 2^(s1 + s2), beside
// a z of 54 bits whose ulp is 2 2^(s1 + s2) (or 4), so z + x y lies within
// x y's low part of a midpoint and only the rounding to odd decides it.
FmaNearTie(a: u64, b: u64, c: u64, d: u64, s1: int, s2: int): (x: f64, y: f64, z: f64) {
    var x := 0.0
    var y := 0.0
    if d & 1 == 0 {
        j := Cast[f64](Cast[i64]((a & 0x3) + 1))
        x = (1.0 + j * 0.0000000074505805969238281) * FmaPow2(s1)
        y = (1.0 - j * 0.0000000074505805969238281) * FmaPow2(s2)
    } else {
        x0 := f64.FromBits(0x3FF0000000000000 | (d & 0x000FFFFFFFFFFFFF))
        x = x0 * FmaPow2(s1)
        y = (1.0 / x0) * FmaPow2(s2)
    }
    m := Cast[f64](Cast[i64](b >> 13))
    zm := (9007199254740992.0 + 2.0 * m + Cast[f64](Cast[i64](c & 1))) * FmaPow2(s1 + s2 + Cast[int](c >> 1 & 1))
    z := if (c >> 2) & 1 == 0 { zm } else { -zm }
    if (c >> 3) & 1 == 1 {
        x = -x
    }
    return (x: x, y: y, z: z)
}

// FmaCase is input kind 0 to 8 from the random words a, b, c and d.
FmaCase(kind: int, a: u64, b: u64, c: u64, d: u64): (x: f64, y: f64, z: f64) {
    if kind == 0 {
        return (x: FmaF64(a, -5, 5), y: FmaF64(b, -5, 5), z: FmaF64(c, -5, 5))
    }
    if kind == 1 {
        // Massive cancellation: z is -(x y) rounded, moved by up to 4 ulps.
        x := FmaF64(a, -60, 60)
        y := FmaF64(b, -60, 60)
        p := x * y
        return (x: x, y: y, z: f64.FromBits((-p).Bits() +% (d & 7) -% 3))
    }
    if kind == 2 {
        // Short mantissas: exact ties are frequent.
        x := Cast[f64](Cast[i64](a >> 37) | 1) * FmaPow2(Cast[int](d & 15) - 8)
        y := Cast[f64](Cast[i64](b >> 37) | 1)
        var z := Cast[f64](Cast[i64](c >> 11)) * FmaPow2(Cast[int]((d >> 8) & 63) - 20)
        if d & 0x10000 != 0 {
            z = -z
        }
        return (x: x, y: y, z: z)
    }
    if kind == 3 {
        // The window's edges and just outside.
        return (x: FmaF64(a, -258, -250), y: FmaF64(b, 250, 259), z: FmaF64(c, -1022, 1000))
    }
    if kind == 4 {
        // Tiny products beside a tiny or subnormal z.
        return (x: FmaF64(a, -256, -240), y: FmaF64(b, -256, -240), z: f64.FromBits(c & 0x801FFFFFFFFFFFFF))
    }
    if kind == 5 {
        return (x: FmaF64(a, -1022, 1023), y: FmaF64(b, -1022, 1023), z: FmaF64(c, -1022, 1023))
    }
    if kind == 6 {
        // z dominant, or the product, by about 53 to 110 bits.
        return (x: FmaF64(a, -5, 5), y: FmaF64(b, -5, 5), z: FmaF64(c, -120, 120))
    }
    if kind == 8 {
        // Ties at z's precision: x y rounded is an odd multiple of 2^q and
        // z a 53-bit multiple of 2^(q + 1), so z plus x y rounded is a
        // midpoint and only x y's low part decides.
        var x := 1.0000000074505805969238281 * FmaPow2(Cast[int]((a >> 20) & 31) - 16)
        if (a >> 40) & 1 == 1 {
            x = -x
        }
        y := FmaF64(b, -20, 20)
        ub := (x * y).Bits()
        var mant := (ub & 0x000FFFFFFFFFFFFF) | 0x0010000000000000
        var tz := 0
        for mant & 1 == 0 {
            mant = mant >> 1
            tz += 1
        }
        q := Cast[int]((ub >> 52) & 0x7FF) - 1023 - 52 + tz
        zsign := (c >> 63) << 63
        return (x: x, y: y, z: f64.FromBits(zsign | (Cast[u64](q + 1 + 52 + 1023) << 52) | (c & 0x000FFFFFFFFFFFFF)))
    }
    return FmaNearTie(a, b, c, d, Cast[int]((a >> 20) & 63) - 32, Cast[int]((a >> 30) & 63) - 32)
}

// FmaHwBits is FmaHw's bits where the CPU has fma (the instruction,
// called directly), else FmaSoft's.
FmaHwBits(x: f64, y: f64, z: f64): u64 {
    if !simd.Cpu().Has("fma") {
        return FmaSoft(x, y, z).Bits()
    }
    var r := 0.0
    unsafe "Cpu() reports fma, which FmaHw's clause names" {
        r = FmaHw(x, y, z)
    }
    return r.Bits()
}

// FmaAgrees reports whether FmaSoft, FmaHw (where the CPU has fma) and Fma
// give FmaInt's bits.
FmaAgrees(x: f64, y: f64, z: f64): bool {
    want := FmaInt(x, y, z).Bits()
    return FmaSoft(x, y, z).Bits() == want && FmaHwBits(x, y, z) == want && Fma(x, y, z).Bits() == want
}

test "Fma's paths give FmaInt's bits on every input kind" {
    var st: u64 = 0x2545F4914F6CDD1D
    for i in 0..27_000 {
        st = st +% 0x9E3779B97F4A7C15
        a := FmaMix(st)
        st = st +% 0x9E3779B97F4A7C15
        b := FmaMix(st)
        st = st +% 0x9E3779B97F4A7C15
        c := FmaMix(st)
        st = st +% 0x9E3779B97F4A7C15
        d := FmaMix(st)
        g := FmaCase(i % 9, a, b, c, d)
        Assert(FmaAgrees(g.x, g.y, g.z))
    }
}

test "Fma's paths give FmaInt's bits at the edges of the fast path's window" {
    // x's and y's exponents: the window is [-255, 256], so -256 and 257 lie
    // just outside; z's is below 1000, zero and subnormal included.
    exps: [8]int = [-257, -256, -255, -254, 255, 256, 257, 258]
    var st: u64 = 0x6A09E667F3BCC909
    for i in 0..8 {
        for j in 0..8 {
            for k in 0..12 {
                st = st +% 0x9E3779B97F4A7C15
                a := FmaMix(st)
                st = st +% 0x9E3779B97F4A7C15
                b := FmaMix(st)
                st = st +% 0x9E3779B97F4A7C15
                c := FmaMix(st)
                x := FmaF64(a, exps[i], exps[i])
                y := FmaF64(b, exps[j], exps[j])
                p := x * y
                zs: [12]f64 = [0.0, -0.0, f64.FromBits(1), f64.FromBits(0x800FFFFFFFFFFFFF), FmaF64(c, 998, 998),
                    FmaF64(c, 999, 999), FmaF64(c, 1000, 1000), FmaF64(c, -1022, -1022), -p,
                    f64.FromBits((-p).Bits() +% 1), f64.FromBits(p.Bits() -% 1), FmaF64(c, -60, 60) * p]
                Assert(FmaAgrees(x, y, zs[k]))
                Assert(FmaAgrees(x, y, -zs[k]))
            }
        }
    }
    // Built near-ties with x's or y's exponent at a window edge.
    for i in 0..4_000 {
        st = st +% 0x9E3779B97F4A7C15
        a := FmaMix(st)
        st = st +% 0x9E3779B97F4A7C15
        b := FmaMix(st)
        st = st +% 0x9E3779B97F4A7C15
        c := FmaMix(st)
        st = st +% 0x9E3779B97F4A7C15
        d := FmaMix(st)
        e1 := exps[2 + Trunc[int](a >> 61) % 4]
        e2 := exps[2 + Trunc[int](b >> 61) % 4]
        g := FmaNearTie(a, b, c, d, if i % 2 == 0 { e1 } else { Cast[int]((a >> 20) & 63) - 32 }, e2)
        Assert(FmaAgrees(g.x, g.y, g.z))
    }
}

test "Fma's NaN results are Nan's bits on both paths" {
    // NaN operands of either sign, quiet and signalling, with payloads, and
    // the invalid operations (inf * 0, inf - inf), each in every position.
    nans: [4]u64 = [0x7FF8000000000123, 0xFFF8000000000001, 0x7FF0000000000001, 0xFFFFFFFFFFFFFFFF]
    inf := f64.FromBits(0x7FF0000000000000)
    for i in 0..4 {
        n := f64.FromBits(nans[i])
        cases: [9][3]f64 = [[n, 1.0, 1.0], [1.0, n, 1.0], [1.0, 1.0, n], [n, n, n], [inf, 0.0, 1.0],
            [0.0, -inf, n], [inf, 1.0, -inf], [-inf, -1.0, -inf], [inf, -0.0, inf]]
        for k in 0..9 {
            x := cases[k][0]
            y := cases[k][1]
            z := cases[k][2]
            Assert(FmaSoft(x, y, z).Bits() == Nan.Bits())
            Assert(FmaHwBits(x, y, z) == Nan.Bits())
            Assert(Fma(x, y, z).Bits() == Nan.Bits())
            Assert(FmaInt(x, y, z).Bits() == Nan.Bits())
        }
    }
}

test "Fma's paths give FmaInt's bits on zeros, infinities and subnormals" {
    // Signed zeros, infinities, the least and largest subnormals, the
    // least and largest normals and two ordinary values, in every position
    // (zero results' signs and inf * 0 included), then beside random z.
    sp: [12]u64 = [0x0000000000000000, 0x8000000000000000, 0x7FF0000000000000, 0xFFF0000000000000,
        0x0000000000000001, 0x8000000000000001, 0x000FFFFFFFFFFFFF, 0x800FFFFFFFFFFFFF,
        0x0010000000000000, 0xFFEFFFFFFFFFFFFF, 0x3FF0000000000000, 0xBFF8000000000000]
    for i in 0..12 {
        for j in 0..12 {
            for k in 0..12 {
                Assert(FmaAgrees(f64.FromBits(sp[i]), f64.FromBits(sp[j]), f64.FromBits(sp[k])))
            }
        }
    }
    var st: u64 = 0x510E527FADE682D1
    for i in 0..12 {
        for j in 0..12 {
            for _ in 0..8 {
                st = st +% 0x9E3779B97F4A7C15
                z := FmaF64(FmaMix(st), -1022, 1023)
                Assert(FmaAgrees(f64.FromBits(sp[i]), f64.FromBits(sp[j]), z))
                Assert(FmaAgrees(f64.FromBits(sp[i]), z, f64.FromBits(sp[j])))
            }
        }
    }
}

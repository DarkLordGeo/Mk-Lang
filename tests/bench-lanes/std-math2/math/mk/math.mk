// math, Mk: std.math's f64 and f32 functions over 4,096 inputs built from
// splitmix64 bits (README.md), reps passes, pass r starting at input r so
// no pass repeats the last one's order. The checksum adds bits >> 32 of
// each f64 result (bits >> 16 of each f32 result) with wrapping adds, so
// the columns agree although their libraries may differ by an ulp
// (README.md). Every call is direct; std.math is pure Mk with no cpu
// dispatch.
// Usage: math <fn> <reps> <seed>.

import std.math

const N: int = 4096

Mix(z0: u64): u64 {
    var z := z0
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

// F64 is an f64 with z's mantissa, an exponent in [lo, hi] from z's top
// bits, and z's bit 52 as its sign when signed.
F64(z: u64, lo: int, hi: int, signed: bool): f64 {
    span := Cast[u64](hi - lo + 1)
    e := Cast[u64](lo + 1023) + (z >> 53) % span
    sign := if signed { (z & 0x0010000000000000) << 11 } else { 0 }
    return f64.FromBits(sign | (e << 52) | (z & 0x000FFFFFFFFFFFFF))
}

// F32 is the f32 form of F64.
F32(z: u64, lo: int, hi: int, signed: bool): f32 {
    span := Cast[u64](hi - lo + 1)
    e := Cast[u64](lo + 127) + (z >> 53) % span
    sign := if signed { (z & 0x0010000000000000) >> 21 } else { 0 }
    return f32.FromBits(Trunc[u32](sign | (e << 23) | (z & 0x7FFFFF)))
}

// Spec is one function's input ranges: exponents [lo, hi] and the sign.
struct Spec {
    lo: int
    hi: int
    signed: bool
}

SpecOf(name: str): Spec {
    if name == "exp" || name == "expf" {
        return Spec(lo: -10, hi: 5, signed: true)
    }
    if name == "log" || name == "logf" {
        return Spec(lo: -100, hi: 100, signed: false)
    }
    if name == "sin" || name == "sinf" {
        return Spec(lo: -10, hi: 2, signed: true)
    }
    if name == "sinlarge" {
        return Spec(lo: 20, hi: 60, signed: true)
    }
    if name == "pow" {
        return Spec(lo: -4, hi: 3, signed: false)
    }
    if name == "sinh" {
        return Spec(lo: -10, hi: 4, signed: true)
    }
    if name == "cbrt" {
        return Spec(lo: -200, hi: 200, signed: true)
    }
    if name == "fma" {
        return Spec(lo: -5, hi: 5, signed: true)
    }
    return Spec(lo: -10, hi: 10, signed: true) // atan2
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    name := sys.args[1]
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Trunc[u64](try sys.args[3].ParseInt())
    sp := SpecOf(name)
    f32_row := name == "expf" || name == "logf" || name == "sinf"
    xs := try sys.arena.Slice[f64](N)
    ys := try sys.arena.Slice[f64](N)
    zs := try sys.arena.Slice[f64](N)
    fs := try sys.arena.Slice[f32](N)
    for i in 0..N {
        st = st +% 0x9E3779B97F4A7C15
        a := Mix(st)
        st = st +% 0x9E3779B97F4A7C15
        b := Mix(st)
        st = st +% 0x9E3779B97F4A7C15
        c := Mix(st)
        xs[i] = F64(a, sp.lo, sp.hi, sp.signed)
        if f32_row {
            fs[i] = F32(a, sp.lo, sp.hi, sp.signed)
        }
        // pow's exponent y in (-32, 32); the others' second input shares x's ranges.
        ys[i] = if name == "pow" { F64(b, -4, 4, true) } else { F64(b, sp.lo, sp.hi, sp.signed) }
        zs[i] = F64(c, sp.lo, sp.hi, sp.signed)
    }
    var acc: u64 = 0
    for r in 0..reps {
        if name == "exp" {
            for i in 0..N {
                acc = acc +% (math.Exp(xs[(i + r) & (N - 1)]).Bits() >> 32)
            }
        } else if name == "log" {
            for i in 0..N {
                acc = acc +% (math.Log(xs[(i + r) & (N - 1)]).Bits() >> 32)
            }
        } else if name == "sin" || name == "sinlarge" {
            for i in 0..N {
                acc = acc +% (math.Sin(xs[(i + r) & (N - 1)]).Bits() >> 32)
            }
        } else if name == "pow" {
            for i in 0..N {
                k := (i + r) & (N - 1)
                acc = acc +% (math.Pow(xs[k], ys[k]).Bits() >> 32)
            }
        } else if name == "atan2" {
            for i in 0..N {
                k := (i + r) & (N - 1)
                acc = acc +% (math.Atan2(ys[k], xs[k]).Bits() >> 32)
            }
        } else if name == "sinh" {
            for i in 0..N {
                acc = acc +% (math.Sinh(xs[(i + r) & (N - 1)]).Bits() >> 32)
            }
        } else if name == "cbrt" {
            for i in 0..N {
                acc = acc +% (math.Cbrt(xs[(i + r) & (N - 1)]).Bits() >> 32)
            }
        } else if name == "fma" {
            for i in 0..N {
                k := (i + r) & (N - 1)
                acc = acc +% (math.Fma(xs[k], ys[k], zs[k]).Bits() >> 32)
            }
        } else if name == "expf" {
            for i in 0..N {
                acc = acc +% u64(math.ExpF32(fs[(i + r) & (N - 1)]).Bits() >> 16)
            }
        } else if name == "logf" {
            for i in 0..N {
                acc = acc +% u64(math.LogF32(fs[(i + r) & (N - 1)]).Bits() >> 16)
            }
        } else if name == "sinf" {
            for i in 0..N {
                acc = acc +% u64(math.SinF32(fs[(i + r) & (N - 1)]).Bits() >> 16)
            }
        }
    }
    var w := sys.Stdout()
    try Print(mut w, "{acc}\n", (acc))
}

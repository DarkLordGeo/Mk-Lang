// The dispatch pattern through fn values. Pick names a cpu "avx2" function as
// a value inside unsafe, under a Cpu() test, else a baseline one; Scale names
// Fma as a value in a cpu "fma" function (its wrapper carries fma). The const
// C runs the baseline paths (the evaluator's Cpu() is the target's baseline),
// the run takes the fast ones where the machine has them; both compute the
// same lanes (small integers, so v * v + v is exact and equals the fused
// form). Main prints the const, the run, and whether the machine has avx2 and
// fma.
import std.simd

Double8(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] cpu "avx2" {
    return v + v
}

DoubleBase(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] {
    return v + v
}

Pick(): fn(simd.Vec[f32, 8]): simd.Vec[f32, 8] {
    if simd.Cpu().Has("avx2") {
        unsafe "Cpu() reports avx2, which Double8 needs" {
            return Double8
        }
    }
    return DoubleBase
}

ScaleFma(v: simd.Vec[f32, 4]): simd.Vec[f32, 4] cpu "fma" {
    f := simd.Vec[f32, 4].Fma
    return f(v, v, v)
}

ScaleBase(v: simd.Vec[f32, 4]): simd.Vec[f32, 4] {
    return v * v + v
}

PickScale(): fn(simd.Vec[f32, 4]): simd.Vec[f32, 4] {
    if simd.Cpu().Has("fma") {
        unsafe "Cpu() reports fma, which ScaleFma needs" {
            return ScaleFma
        }
    }
    return ScaleBase
}

Kernel(): u64 {
    var h: u64 = 7
    double := Pick()
    scale := PickScale()
    var v := simd.Vec[f32, 8].Iota()
    for _ in 0..3 {
        v = double(v)
    }
    h = h *% 31 +% Cast[u64](v.ReduceSum())
    w := scale(simd.Vec[f32, 4].Iota())
    h = h *% 31 +% Cast[u64](w.ReduceSum())
    return h
}

const C: u64 = Kernel()

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    r := Kernel()
    c := simd.Cpu()
    a := c.Has("avx2") && c.Has("fma")
    try Print(mut out, "{c} {r} {a}\n", (c: C, r, a))
}

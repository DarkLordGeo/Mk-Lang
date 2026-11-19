// A cpu "avx2" function reached through a Cpu() test in unsafe. The const C
// runs the baseline path (the evaluator's Cpu() is the target's baseline),
// the run takes the avx2 path where the machine has it; both double the
// lanes, so the two agree. Main prints the const, the run and whether the
// machine has avx2.
import std.simd

Double8(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] cpu "avx2" {
    return v + v
}

Double(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] {
    if simd.Cpu().Has("avx2") {
        unsafe "Cpu() reports avx2, which Double8 needs" {
            return Double8(v)
        }
    }
    return v + v
}

Kernel(): u64 {
    var h: u64 = 7
    c := simd.Cpu()
    if c.Has("sse2") { h = h *% 31 +% 1 }
    if c.HasAll(&simd.CpuFeatures.Of("sse4.2, popcnt")) { h = h *% 31 +% 2 }
    v := simd.Vec[f32, 8].Iota()
    d := Double(Double(v))
    h = h *% 31 +% Cast[u64](d.ReduceSum())
    return h
}

const C: u64 = Kernel()

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    r := Kernel()
    a := simd.Cpu().Has("avx2")
    try Print(mut out, "{c} {r} {a}\n", (c: C, r, a))
}

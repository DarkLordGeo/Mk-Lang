// SumN[8] is called from Sum8, whose
// cpu clause is x86-64-v3, and from Sum8Base, which has none, so mono makes
// two instances and the first is compiled with avx2 (and inlines into
// Sum8 only under its features). The const C runs the baseline path (the
// evaluator's Cpu() is the target's baseline); the run takes the x86-64-v3
// path where the machine has it. Both add in the same order, so the two
// agree. Main prints the const, the run and whether the machine has avx2.
import std.simd

SumN[const N: int](xs: []u32): u32
    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 {
    var acc := simd.Vec[u32, N].Zero()
    var i := 0
    for i + N <= xs.Len() {
        acc = acc +% simd.Vec[u32, N].FromSlice(xs, i)
        i += N
    }
    var total := acc.ReduceSumWrap()
    for i < xs.Len() {
        total = total +% xs[i]
        i += 1
    }
    return total
}

Sum8(xs: []u32): u32 cpu "x86-64-v3" {
    return SumN[8](xs)
}

Sum8Base(xs: []u32): u32 {
    return SumN[8](xs)
}

Sum(xs: []u32): u32 {
    if simd.Cpu().HasAll(&simd.CpuFeatures.Of("x86-64-v3")) {
        unsafe "Cpu() reports x86-64-v3, which Sum8 needs" {
            return Sum8(xs)
        }
    }
    return Sum8Base(xs)
}

Kernel(): u64 {
    var a: [203]u32 = [0; 203]
    var s: u32 = 12345
    for j in 0..203 {
        s = s *% 1103515245 +% 12345
        a[j] = s
    }
    return u64(Sum(a[0..])) *% 31 +% u64(Sum8Base(a[3..]))
}

const C: u64 = Kernel()

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    r := Kernel()
    a := simd.Cpu().Has("avx2")
    try Print(mut out, "{c} {r} {a}\n", (c: C, r, a))
}

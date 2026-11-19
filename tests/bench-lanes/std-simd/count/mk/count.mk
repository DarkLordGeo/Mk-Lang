// count, Mk, SIMD table: the kernel of ../README.md with std.simd.Count
// (std/simd/bytes.mk: compare masks subtracted from four byte counters, a
// step of four vectors, folded with ReduceSumWide every 255 steps; the
// 16-lane form below 64 bytes or without avx2, the 32-lane form, compiled
// with avx2, when Cpu() reports it). The buffer is an arena slice (BS-4's
// labelled exception). Usage: count <size> <reps> <seed>.

import std.simd

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    buf := try sys.arena.Slice[u8](n)
    for i in 0..n {
        buf[i] = Trunc[u8]((Cast[u64](i) *% 0x9e3779b97f4a7c15 +% st) >> 60)
    }
    var total: u64 = 0
    for _ in 0..reps {
        st = st +% 0x9e3779b97f4a7c15
        c := simd.Count(buf[0..n], Trunc[u8](Mix(st) & 15))
        total = total *% 31 +% Cast[u64](c)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

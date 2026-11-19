// dot, Mk, SIMD table: the kernel of tests/bench/simd/dot's README with
// std.simd.Dot (std/simd/float.mk: the 16 partial sums in one Vec[f32, 16],
// products rounded before their sums, ReduceSum's pairwise tree, then the
// tail in order; four xmm registers at the baseline, two ymm under avx2,
// the same bits either way). The buffers are arena slices (BS-4's labelled
// exception). Usage: dot <n> <reps> <seed>. Prints the checksum.

import std.simd

Bits(x: f32): u64 {
    return Cast[u64](simd.Vec[f32, 2].Splat(x).Reinterpret[u32, 2]().Lane(0))
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    x := try sys.arena.Slice[f32](n)
    y := try sys.arena.Slice[f32](n)
    for i in 0..n {
        u := Cast[u64](i)
        x[i] = Cast[f32]((u *% 0x9e3779b97f4a7c15 +% seed) >> 48) * 0.0000152587890625 - 0.5
        y[i] = Cast[f32]((u *% 0xbf58476d1ce4e5b9 +% seed) >> 48) * 0.0000152587890625 - 0.5
    }
    var total: u64 = 0
    for r in 0..reps {
        x[r % n] = x[r % n] + 1.0
        total = total +% Bits(simd.Dot(x[0..n], y[0..n]))
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

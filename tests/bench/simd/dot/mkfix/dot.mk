// dot, Mk, SIMD table: the kernel of README.md with std.simd's Vec, at the
// build's fixed CPU target and no run-time dispatch
//: the 16 partial sums in one Vec[f32, 16], each product rounded
// before its sum (Mk never fuses), ReduceSum's pairwise tree, then
// the tail in order. That is std.simd.Dot's baseline body (std/simd/
// float.mk, DotOf with no head) without its Cpu() test, so the same text
// serves every variant: LLVM keeps the 16 sums in four xmm registers at
// x86-64-v2 and in two ymm registers under --cpu x86-64-v3, the width of
// the other columns, with the same bits. The buffers are arena slices at 16
// mod 64, whose alignment Mk code cannot set without std.raw (labelled
// alignment exception, README.md): at v2 no 16-byte load crosses a cache
// line, and at v3 half of the 32-byte loads of each buffer do (those at 48
// mod 64), a cost the 64-byte-aligned columns do not pay. Usage: dot <n>
// <reps> <seed>. Prints the checksum.

import std.simd

type V = simd.Vec[f32, 16]

Bits(x: f32): u64 {
    return Cast[u64](simd.Vec[f32, 2].Splat(x).Reinterpret[u32, 2]().Lane(0))
}

// Dot is the sum of a[i] * b[i] in README.md's order; a.Len() == b.Len().
Dot(a: []f32, b: []f32): f32 {
    n := a.Len()
    q := n - n % 16
    var acc := V.Zero()
    var i := 0
    for i + 16 <= q {
        acc = acc + V.FromSlice(a, i) * V.FromSlice(b, i)
        i += 16
    }
    var r: f32 = acc.ReduceSum()
    for i < n {
        r += a[i] * b[i]
        i += 1
    }
    return r
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
        total = total +% Bits(Dot(x[0..n], y[0..n]))
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

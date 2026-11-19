// compare, Mk, SIMD table: the kernel of ../README.md with
// std.simd.Compare (std/simd/bytes.mk: four vectors XOR-ed and tested once
// a step, the first differing lane by Ctz64 of the != mask, ordered
// unsigned, then the lengths; the 16-lane form below 64 bytes or without
// avx2, the 32-lane form, compiled with avx2, when Cpu() reports it). The
// buffers are arena slices (BS-4's labelled exception). Usage: compare
// <size> <reps> <seed>.

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
    a := try sys.arena.Slice[u8](n)
    b := try sys.arena.Slice[u8](n)
    for i in 0..n {
        a[i] = Trunc[u8]((Cast[u64](i) *% 0x9e3779b97f4a7c15 +% st) >> 56)
        b[i] = a[i]
    }
    var span: u64 = 64
    if n < 64 {
        span = Cast[u64](n)
    }
    var total: u64 = 0
    for _ in 0..reps {
        st = st +% 0x9e3779b97f4a7c15
        m := Mix(st)
        pos := n - 1 - Cast[int](m % span)
        d := Trunc[u8]((m >> 32) & 1)
        b[pos] = b[pos] ^ d
        total = total *% 31 +% Cast[u64](simd.Compare(a[0..n], b[0..n]) + 1)
        b[pos] = b[pos] ^ d
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

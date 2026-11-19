// indexany, Mk, SIMD table: the kernel of ../README.md with
// std.simd.IndexAny over a set of 8 bytes (std/simd/bytes.mk: Truffle, two
// 16-byte tables by low nibble chosen by the top bit, ANDed with a bit by
// high nibble, one vector a step, then one overlapping load; the 16-lane
// form below 64 bytes or without avx2, the 32-lane form, compiled with avx2, when Cpu() reports it,
// and from 256 bytes the 64-lane form, compiled with avx512bw, when
// Cpu() reports that). The buffer is an arena slice (BS-4's
// labelled exception). Usage: indexany <size> <reps> <seed>.

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
    var set: [8]u8 = [0x09, 0x0A, 0x0D, 0x20, 0x22, 0x2C, 0x3A, 0x3D]
    buf := try sys.arena.Slice[u8](n)
    for i in 0..n {
        buf[i] = 0x40 | Trunc[u8]((Cast[u64](i) *% 131 +% st) & 0x3F)
    }
    var span: u64 = 64
    if n < 64 {
        span = Cast[u64](n)
    }
    var total := 0
    for _ in 0..reps {
        st = st +% 0x9e3779b97f4a7c15
        m := Mix(st)
        pos := n - 1 - Cast[int](m % span)
        old := buf[pos]
        buf[pos] = set[Cast[int]((m >> 32) & 7)]
        i := simd.IndexAny(buf[0..n], set[0..8]) else { -1 }
        total += i
        buf[pos] = old
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

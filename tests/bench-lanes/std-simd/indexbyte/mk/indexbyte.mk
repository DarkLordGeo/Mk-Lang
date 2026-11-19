// indexbyte, Mk, SIMD table: the kernel of tests/bench/simd/indexbyte's
// README with std.simd.IndexByte (std/simd/bytes.mk: four vectors a step,
// their compares OR-ed and tested once, then one vector, then one
// overlapping load; the 16-lane form below 64 bytes or without avx2, the
// 32-lane form, compiled with avx2, when Cpu() reports it,
// and from 256 bytes the 64-lane form, compiled with avx512bw, when
// Cpu() reports that). The buffer is an
// arena slice, whose alignment Mk code cannot set without std.raw (BS-4's
// labelled exception, tests/bench/README.md). Usage: indexbyte <size> <reps> <seed>.

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
        buf[i] = Trunc[u8]((Cast[u64](i) *% 131 +% st) & 0x7F)
    }
    var span: u64 = 64
    if n < 64 {
        span = Cast[u64](n)
    }
    var total := 0
    for _ in 0..reps {
        st = st +% 0x9e3779b97f4a7c15
        pos := n - 1 - Cast[int](Mix(st) % span)
        old := buf[pos]
        buf[pos] = 0xFF
        i := simd.IndexByte(buf[0..n], 0xFF) else { -1 }
        total += i
        buf[pos] = old
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

// indexbyte, Mk, SIMD table: the kernel of README.md with std.simd's Vec and
// Mask, at the build's fixed CPU target and no run-time dispatch,
//, the loop of indexbyte.c: four vectors a step, their
// compares OR-ed and tested once (Mask.Any), then one vector a step
// (Mask.FirstSet, the bit scan of C's movemask and __builtin_ctz), then
// bytes. This package is the v2 variants', L = 16 (Vec[u8, 16]: one xmm
// register at x86-64-v2); ../mkfix is the v3 variants', the same text with L
// = 32 (one ymm under --cpu x86-64-v3), since Mk has no compile-time fact of
// the build's CPU (Target.vector_bits never follows --cpu) and C, Rust and
// Zig choose the width from the target at compile time. The buffer is an
// arena slice, whose alignment Mk code cannot set without std.raw (labelled
// alignment exception, README.md). Usage: indexbyte <size> <reps> <seed>.

import std.simd

const L: int = 16

type V = simd.Vec[u8, L]

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// IndexByte is the offset of the first byte of s equal to b, or -1.
IndexByte(s: []u8, b: u8): int {
    n := s.Len()
    nb := V.Splat(b)
    var i := 0
    for i + 4 * L <= n {
        w := s[i..i + 4 * L]
        e := (V.FromSlice(w, 0) == nb) | (V.FromSlice(w, L) == nb) | (V.FromSlice(w, 2 * L) == nb) | (V.FromSlice(w, 3 * L) == nb)
        if e.Any() {
            break
        }
        i += 4 * L
    }
    for i + L <= n {
        if k := (V.FromSlice(s, i) == nb).FirstSet() {
            return i + k
        }
        i += L
    }
    for i < n {
        if s[i] == b {
            return i
        }
        i += 1
    }
    return -1
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
        total += IndexByte(buf[0..n], 0xFF)
        buf[pos] = old
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

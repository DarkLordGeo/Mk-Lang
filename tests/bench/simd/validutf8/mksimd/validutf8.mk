// validutf8, Mk, SIMD table: the kernel of tests/bench/simd/validutf8's
// README with std.simd.ValidUtf8 (std/simd/utf8.mk: Keiser and Lemire's
// lookup algorithm, four vectors a step with an ASCII test; the 16-lane form
// below 64 bytes or without avx2, the 32-lane form, compiled with avx2, when Cpu() reports it,
// and from 256 bytes the 64-lane form, compiled with avx512bw, when
// Cpu() reports that). The rest is the row's original Mk program. The
// buffer is an arena slice (labelled alignment exception).
// Usage: validutf8 <size> <reps> <seed>. Prints the checksum.

import std.simd

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// The 16 encodings of ../README.md, four bytes each; entry k is 1 + k / 4
// bytes long.
const Enc: [64]u8 = [
    0x61, 0, 0, 0, 0x20, 0, 0, 0, 0x30, 0, 0, 0, 0x7E, 0, 0, 0,
    0xC2, 0x80, 0, 0, 0xDF, 0xBF, 0, 0, 0xC3, 0xA9, 0, 0, 0xD0, 0x96, 0, 0,
    0xE0, 0xA0, 0x80, 0, 0xED, 0x9F, 0xBF, 0, 0xEE, 0x80, 0x80, 0, 0xE4, 0xB8, 0x96, 0,
    0xF0, 0x90, 0x80, 0x80, 0xF4, 0x8F, 0xBF, 0xBF, 0xF0, 0x9F, 0x98, 0x80, 0xF3, 0xA0, 0x80, 0x80,
]

Valid(b: []u8): bool {
    return simd.ValidUtf8(b)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    var blen := n
    if blen > 65536 {
        blen = 65536
    }
    blk := try sys.arena.Slice[u8](blen)
    var p := 0
    for {
        st = st +% 0x9e3779b97f4a7c15
        k := Cast[int](Mix(st) & 15)
        l := 1 + k / 4
        if p + l > blen {
            break
        }
        for j in 0..l {
            blk[p + j] = Enc[k * 4 + j]
        }
        p += l
    }
    (mut blk[p..blen]).Fill(0x61)
    buf := try sys.arena.Slice[u8](n)
    var i := 0
    for i + blen <= n {
        (mut buf[i..i + blen]).CopyFrom(blk[0..blen])
        i += blen
    }
    (mut buf[i..n]).Fill(0x61)
    var span: u64 = 64
    if n < 64 {
        span = Cast[u64](n)
    }
    var total: u64 = 0
    for _ in 0..reps {
        st = st +% 0x9e3779b97f4a7c15
        z := Mix(st)
        pos := n - 1 - Cast[int]((z >> 1) % span)
        old := buf[pos]
        if z & 1 == 1 {
            buf[pos] = Trunc[u8](z >> 56)
        }
        var ok: u64 = 0
        if Valid(buf[0..n]) {
            ok = 1
        }
        total = total *% 31 +% ok
        buf[pos] = old
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

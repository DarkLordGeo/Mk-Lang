// validutf8, Mk, SIMD table: the kernel of ../README.md with std's
// validator as it ships, str.From (std/core/core_utf8.mk), in
// validutf8-l1v2 only. Below 256 bytes it is scalar: ASCII eight bytes per
// load, every other byte through Unicode §3.9's per-lead ranges, like
// Go's utf8.Valid. std.core's ValidUtf8 hands 256 bytes or more
// to std.simd.ValidUtf8, which chooses its vector form at run time, so the
// cell is labelled dispatching (the variant's dispatching field). This
// file calls no std.simd routine itself.
// Usage: validutf8 <size> <reps> <seed>. Prints the checksum.

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
    s := str.From(b) else {
        return false
    }
    return s.Len() == b.Len()
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

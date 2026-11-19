// textset, Mk: the task of ../README.md with std's HeapText.Set over the
// process heap: one text per round, set n times to slices of 0 to 299
// bytes of a 4,096-byte source. Usage: textset <n> <rounds> <seed>.
// Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Round(a: &Alloc, src: str, n: int, seed: u64): OutOfMemory!u64 {
    var t := HeapText.New(a)
    onerr t.Free(a)
    var sum: u64 = 0
    var st := seed
    for _ in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        v := Mix(st)
        k := Cast[int](v % 300)
        off := Cast[int]((v >> 16) % 3796)
        try t.Set(a, src[off..off + k])
        s := t.Str()
        sum = sum +% Cast[u64](s.Len())
        if s.Len() > 0 {
            sum = sum +% Cast[u64](s.Byte(0)) *% 7 +% Cast[u64](s.Byte(s.Len() - 1))
        }
    }
    t.Free(a)
    return sum
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    var buf: [4096]u8 = [0; 4096]
    var st := seed ^ 0x5555555555555555
    for i in 0..4096 {
        st = st +% 0x9e3779b97f4a7c15
        buf[i] = Trunc[u8](Mix(st) % 26) + 0x61
    }
    src := str.From(buf[..]).Must()
    var total: u64 = 0
    for r in 0..rounds {
        total = total +% try Round(sys.heap.Alloc(), src, n, seed +% Cast[u64](r))
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

// deepfree, Mk: the task of ../README.md with std's deep Free: per round
// a HeapList[HeapText] of n texts of 0 to 63 bytes, each a slice of a
// 4,096-byte source, read back once, then released by the list's own
// Free, which frees every text before the storage. Usage:
// deepfree <n> <rounds> <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Round(a: &Alloc, src: str, n: int, seed: u64): OutOfMemory!u64 {
    var l := HeapList[HeapText].New(a)
    onerr l.Free(a)
    var st := seed
    for _ in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        v := Mix(st)
        k := Cast[int](v % 64)
        off := Cast[int]((v >> 16) % 4032)
        var t := HeapText.New(a)
        onerr t.Free(a)
        try t.Set(a, src[off..off + k])
        try l.Push(a, t)
    }
    var sum: u64 = 0
    for i in 0..l.Len() {
        s := l[i].Str()
        sum = sum +% Cast[u64](s.Len())
        if s.Len() > 0 {
            sum = sum +% Cast[u64](s.Byte(0)) *% 7 +% Cast[u64](s.Byte(s.Len() - 1))
        }
    }
    l.Free(a)
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

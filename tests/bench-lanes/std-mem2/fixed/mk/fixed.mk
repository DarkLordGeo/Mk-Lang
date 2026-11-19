// fixed, Mk: the workload of ../README.md with std.mem.Fixed over a 64 KiB
// stack buffer and a HeapList[u64] per round. The sums wrap by design (+%),
// as splitmix64 does.
// Usage: fixed <rounds> <seed>. Prints the checksum.

import std.mem

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// Round builds one list of k values in f, sums it and gives it back.
Round(f: &mem.Fixed, r: u64, k: int): OutOfMemory!u64 {
    var xs := HeapList[u64].New(f.Alloc())
    onerr xs.Free(f.Alloc())
    for i in 0..k {
        try xs.Push(f.Alloc(), r +% Cast[u64](i))
    }
    var s: u64 = 0
    for x in xs.Items() {
        s = s +% x
    }
    xs.Free(f.Alloc())
    return s
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    rounds := Cast[int](try sys.args[1].ParseInt())
    var st := Cast[u64](try sys.args[2].ParseInt())
    var buf: [65536]u8 = [0; 65536]
    var f := mem.Fixed.New(mut buf[..])
    var total: u64 = 0
    for _ in 0..rounds {
        st = st +% 0x9e3779b97f4a7c15
        r := Mix(st)
        total = total +% try Round(&f, r, Trunc[int](r & 511) + 1)
        f.Reset()
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

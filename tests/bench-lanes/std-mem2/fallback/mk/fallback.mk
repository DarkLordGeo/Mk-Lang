// fallback, Mk: the workload of ../README.md with std.mem.Fallback over a
// 1 KiB stack buffer, then std's general heap, and a HeapList[u64] per
// round. The sums wrap by design (+%), as splitmix64 does.
// Usage: fallback <rounds> <seed>. Prints the checksum.

import std.mem

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// Round builds one list of k values in b, sums it and gives it back.
Round(b: &mem.Fallback, r: u64, k: int): OutOfMemory!u64 {
    var xs := HeapList[u64].New(b.Alloc())
    onerr xs.Free(b.Alloc())
    for i in 0..k {
        try xs.Push(b.Alloc(), r +% Cast[u64](i))
    }
    var s: u64 = 0
    for x in xs.Items() {
        s = s +% x
    }
    xs.Free(b.Alloc())
    return s
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    rounds := Cast[int](try sys.args[1].ParseInt())
    var st := Cast[u64](try sys.args[2].ParseInt())
    var buf: [1024]u8 = [0; 1024]
    var b := mem.Fallback.New(sys.heap.Alloc(), mut buf[..])
    var total: u64 = 0
    for _ in 0..rounds {
        st = st +% 0x9e3779b97f4a7c15
        r := Mix(st)
        var k := Trunc[int](r & 63) + 1
        if r >> 60 == 0 {
            k = Trunc[int](r & 1023) + 1
        }
        total = total +% try Round(&b, r, k)
        b.Reset()
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

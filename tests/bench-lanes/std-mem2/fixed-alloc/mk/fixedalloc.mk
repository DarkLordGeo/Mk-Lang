// fixed-alloc, Mk: the workload of ../README.md with std.mem.Fixed over a
// 64 KiB stack buffer: two Box[u64] per iteration, given back in LIFO order,
// so every request goes through the allocator's alloc and free entries. The
// sums wrap by design (+%), as splitmix64 does.
// Usage: fixedalloc <n> <seed>. Prints the checksum.

import std.mem

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    var st := Cast[u64](try sys.args[2].ParseInt())
    var buf: [65536]u8 = [0; 65536]
    f := mem.Fixed.New(mut buf[..])
    var total: u64 = 0
    for _ in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        b1 := try Box[u64].New(f.Alloc(), Mix(st))
        b2 := Box[u64].New(f.Alloc(), st) else {
            b1.Free(f.Alloc())
            fail OutOfMemory
        }
        total = total +% b2.Take(f.Alloc())
        total = total ^ b1.Take(f.Alloc())
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

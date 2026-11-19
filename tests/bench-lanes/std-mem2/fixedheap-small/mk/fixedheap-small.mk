// fixedheap-small, Mk: the task of ../README.md with std.mem.FixedHeap over
// a 2 MiB buffer from the program's arena: each object is a HeapList[u8]
// made from empty by one Extend of its bytes (one allocation and the copy),
// kept in a ring HeapList[HeapList[u8]] of 4,096 live objects in the same
// FixedHeap; each step reads the oldest, puts a new one in its slot with
// Replace, and frees the old one with Free. At the end every live object is
// read and the ring's Free frees them all. The task runs in a function that
// takes the allocator, as code that is given one does.
// Usage: fixedheap-small <steps> <seed>. Prints the checksum.

import std.mem

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

const Live: int = 4096

// Make allocates an object of 8 to 256 bytes copied from src, both drawn
// from st.
Make(a: &Alloc, src: []u8, st: mut u64): OutOfMemory!HeapList[u8] from a {
    *st = *st +% 0x9e3779b97f4a7c15
    v := Mix(*st)
    l := Cast[int](v % 249) + 8
    off := Cast[int]((v >> 16) % 3840)
    var o := HeapList[u8].New(a)
    onerr o.Free(a)
    try o.Extend(a, src[off..off + l])
    return o
}

Read(o: &HeapList[u8]): u64 {
    n := o.Len()
    return Cast[u64](n) +% u64(o[0]) +% u64(o[n - 1])
}

// Churn runs the task over a: the ring of Live objects, steps replacements,
// and the final read of every object.
Churn(a: &Alloc, src: []u8, steps: int, st: mut u64): OutOfMemory!u64 {
    var ring := HeapList[HeapList[u8]].New(a)
    onerr ring.Free(a)
    for _ in 0..Live {
        var o := try Make(a, src, st)
        onerr o.Free(a)
        try ring.Push(a, o)
    }
    var total: u64 = 0
    for j in 0..steps {
        slot := j % Live
        total = total +% Read(&ring[slot])
        o := try Make(a, src, st)
        old := Replace(mut ring[slot], o)
        old.Free(a)
    }
    for i in 0..Live {
        total = total +% Read(&ring[i])
    }
    ring.Free(a)
    return total
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    steps := Cast[int](try sys.args[1].ParseInt())
    var st := Cast[u64](try sys.args[2].ParseInt())
    var buf: [4096]u8 = [0; 4096]
    var gs := st ^ 0x5555555555555555
    for i in 0..4096 {
        gs = gs +% 0x9e3779b97f4a7c15
        buf[i] = Trunc[u8](Mix(gs) % 26) + 0x61
    }
    h := mem.FixedHeap.New(try sys.arena.Slice[u8](2 * 1024 * 1024))
    total := try Churn(h.Alloc(), buf[..], steps, mut st)
    try sys.Print("{total}\n", (total))
}

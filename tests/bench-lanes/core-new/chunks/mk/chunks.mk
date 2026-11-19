// chunks, Mk: the task of ../README.md with std's Slice.ChunksMut and
// Slice.Chunks over a HeapList[u64] of n splitmix64 values. Usage:
// chunks <n> <rounds> <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// Round scales every piece of k in place, then folds every piece read back.
Round(xs: mut HeapList[u64], k: int): u64 {
    var j: u64 = 0
    for c in xs.ChunksMut(k) {
        for e in mut c {
            *e = *e *% 3 +% j
        }
        j = j +% 1
    }
    var sum: u64 = 0
    for c in xs.Chunks(k) {
        sum = sum +% (c[0] ^ c[c.Len() - 1])
        for x in c {
            sum = sum +% x
        }
    }
    return sum
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    a := sys.heap.Alloc()
    var xs := HeapList[u64].New(a)
    onerr xs.Free(a)
    var st := seed
    for _ in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        try xs.Push(a, Mix(st))
    }
    var total: u64 = 0
    for r in 0..rounds {
        k := (r % 16) * 4 + 1
        total = total +% Round(mut xs, k)
    }
    xs.Free(a)
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

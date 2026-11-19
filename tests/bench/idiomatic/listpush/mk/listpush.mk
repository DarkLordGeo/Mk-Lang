// listpush, Mk, idiomatic std: the task of ../README.md with std's heap
// list, HeapList[u64] over sys.heap.Alloc() (std's general heap): per round
// a list from empty, n single Pushes, one Extend by a slice of n/4, a read
// of every element with `for x in l`, then Free. Usage: listpush <n> <rounds>
// <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Round(a: &Alloc, n: int, ext: []u64, st: mut u64): OutOfMemory!u64 {
    var l := HeapList[u64].New(a)
    onerr l.Free(a)
    for _ in 0..n {
        *st = *st +% 0x9e3779b97f4a7c15
        try l.Push(a, Mix(*st))
    }
    try l.Extend(a, ext)
    var sum: u64 = 0
    for x in l {
        sum = sum +% x
    }
    sum = sum +% Cast[u64](l.Len())
    l.Free(a)
    return sum
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    ext := try sys.arena.Slice[u64](n / 4)
    for i in 0..ext.Len() {
        ext[i] = Cast[u64](3 * i + 1)
    }
    var total: u64 = 0
    for _ in 0..rounds {
        total = total +% try Round(sys.heap.Alloc(), n, ext[..], mut st)
    }
    try sys.Print("{total}\n", (total))
}

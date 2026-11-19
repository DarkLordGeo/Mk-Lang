// big-factorial, Mk: an accumulation loop of std.big Mul, then Text. For
// i from 2 to <n>, acc = acc * (i + <seed> mod 7) in a work arena, the
// product copied out through Words into a HeapList[u64] that outlives the
// arena, and the arena reset (the pattern for a value that outlives its
// arena). Then the decimal text of acc: prints its length times 31 plus the
// sum of its digits times their 1-based position, wrapping. Usage:
// big-factorial <n> <seed>.

import std.big

// CopyOut sets acc to the words ws, the copy out of the arena: the slots
// acc already holds are overwritten in one copy and only the words past them
// pushed (HeapList has no bulk append).
CopyOut(h: &Alloc, acc: mut HeapList[u64], ws: []u64): OutOfMemory!void {
    for acc.Len() > ws.Len() {
        _ = acc.Pop()
    }
    m := acc.Len()
    acc.ItemsMut().CopyFrom(ws[..m])
    for k in m..ws.Len() {
        try acc.Push(h, ws[k])
    }
}

export Main(sys: &System): ParseError|OutOfMemory|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    seed := try sys.args[2].ParseInt()
    h := sys.heap.Alloc()
    var acc := HeapList[u64].New(h)
    defer acc.Free(h)
    try acc.Push(h, 1)
    var work := try Arena.New(sys.heap)
    defer work.Free()
    for i in 2..n + 1 {
        x := big.FromWords(acc[..], false)
        f := try big.FromI64(&work, Cast[i64](i) + seed % 7)
        r := try x.Mul(&work, &f)
        try CopyOut(h, mut acc, r.Words())
        work.Reset()
    }
    text := try big.FromWords(acc[..], false).Text(&work, 10)
    b := text.AsBytes()
    var sum: u64 = Cast[u64](b.Len()) *% 31
    for k in 0..b.Len() {
        sum = sum +% u64(b[k] - 0x30) *% Cast[u64](k + 1)
    }
    try sys.Print("{sum}\n", (sum))
}

// big-muldiv, Mk: an accumulation loop of std.big Mul and DivMod. x0 (1024
// words), y (512 words) and d (1024 words, top bit set) are splitmix64
// words of <seed>. Each of <n> steps computes p = x y, (q, r) = p divmod d
// and x = r + q in a work arena, copies x out through Words into a
// HeapList[u64] that outlives the arena, and resets the arena (the
// pattern for a value that outlives its arena). Prints x's low word xor its
// word count. Usage: big-muldiv <n> <seed>.

import std.big

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Fill(a: &Arena, st: mut u64, n: int): OutOfMemory![]u64 from a {
    z := try a.Slice[u64](n)
    for i in 0..n {
        *st = *st +% 0x9e3779b97f4a7c15
        z[i] = Mix(*st)
    }
    return z[..]
}

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
    var st := Cast[u64](try sys.args[2].ParseInt())
    xw := try Fill(sys.arena, mut st, 1024)
    yw := try Fill(sys.arena, mut st, 512)
    dw := try Fill(sys.arena, mut st, 1024)
    dt := try sys.arena.Slice[u64](1024)
    dt.CopyFrom(dw)
    dt[1023] = dt[1023] | 0x8000000000000000
    y := big.FromWords(yw, false)
    d := big.FromWords(dt[..], false)
    h := sys.heap.Alloc()
    var acc := HeapList[u64].New(h)
    defer acc.Free(h)
    for w in xw {
        try acc.Push(h, w)
    }
    var work := try Arena.New(sys.heap)
    defer work.Free()
    for _ in 0..n {
        x := big.FromWords(acc[..], false)
        p := try x.Mul(&work, &y)
        (q, r) := try p.DivMod(&work, &d)
        nx := try r.Add(&work, &q)
        try CopyOut(h, mut acc, nx.Words())
        work.Reset()
    }
    low: u64 = if acc.Len() > 0 { acc[0] } else { 0 }
    sum := low ^ Cast[u64](acc.Len())
    try sys.Print("{sum}\n", (sum))
}

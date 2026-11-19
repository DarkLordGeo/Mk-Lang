// big-conv, Mk: an accumulation loop of std.big Text and Parse. x0 is 2000
// splitmix64 words of <seed>. Each of <n> steps writes x in decimal, parses
// the text back and adds one, in a work arena, copies x out through Words
// into a HeapList[u64] that outlives the arena, and resets the arena (the
// pattern for a value that outlives its arena). Prints x's low word xor the
// sum of the texts' lengths. Usage: big-conv <n> <seed>.

import std.big

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
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

export Main(sys: &System): big.Error|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    var st := Cast[u64](try sys.args[2].ParseInt())
    h := sys.heap.Alloc()
    var acc := HeapList[u64].New(h)
    defer acc.Free(h)
    for _ in 0..2000 {
        st = st +% 0x9e3779b97f4a7c15
        try acc.Push(h, Mix(st))
    }
    var work := try Arena.New(sys.heap)
    defer work.Free()
    var digits: u64 = 0
    for _ in 0..n {
        x := big.FromWords(acc[..], false)
        s := try x.Text(&work, 10)
        v := try big.Parse(&work, s, 10, s.Len())
        one := try big.FromI64(&work, 1)
        nx := try v.Add(&work, &one)
        digits = digits +% Cast[u64](s.Len())
        try CopyOut(h, mut acc, nx.Words())
        work.Reset()
    }
    low: u64 = if acc.Len() > 0 { acc[0] } else { 0 }
    sum := low ^ digits
    try sys.Print("{sum}\n", (sum))
}

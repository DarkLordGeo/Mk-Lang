// big-modpow, Mk: an accumulation loop of std.big ModPow. m, e and x0 are
// 32-word (2048-bit) values from splitmix64 words of <seed> (m odd, top bit
// set). Each of <n> steps computes x = x^e mod m in a work arena, copies
// the result out through Words into a HeapList[u64] that outlives the
// arena, and resets the arena (the pattern for a value that outlives its
// arena). Prints x's low word xor its word count. Usage: big-modpow <n>
// <seed>.

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
    mw := try Fill(sys.arena, mut st, 32)
    ew := try Fill(sys.arena, mut st, 32)
    xw := try Fill(sys.arena, mut st, 32)
    mt := try sys.arena.Slice[u64](32)
    mt.CopyFrom(mw)
    mt[0] = mt[0] | 1
    mt[31] = mt[31] | 0x8000000000000000
    m := big.FromWords(mt[..], false)
    e := big.FromWords(ew, false)
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
        r := try x.ModPow(&work, &e, &m)
        try CopyOut(h, mut acc, r.Words())
        work.Reset()
    }
    low: u64 = if acc.Len() > 0 { acc[0] } else { 0 }
    sum := low ^ Cast[u64](acc.Len())
    try sys.Print("{sum}\n", (sum))
}

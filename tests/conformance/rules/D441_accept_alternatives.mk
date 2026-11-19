// verdict: ok
// rule: D-441
// note: the alternatives for linear elements: Replace hands back the old value, FreeEach a literal that consumes each, FreeEmpty checks at run time, Box.Take gives the value back
Put(a: &Alloc, m: mut HeapMap[int, Res], r: Res): OutOfMemory!void {
    old := try m.Replace(a, 1, r)
    if o := old {
        o.Close()
    }
}

Drain(a: &Alloc, xs: HeapList[Res], m: HeapMap[int, Res]) {
    tag := 7
    xs.FreeEach(a, fn(r: Res) {
        _ = tag
        r.Close()
    })
    m.FreeEach(a, fn(k: int, r: Res) {
        _ = k
        r.Close()
    })
}

Empty(a: &Alloc, xs: HeapList[Res], b: Box[Res]) {
    xs.FreeEmpty(a)
    r := b.Take(a)
    r.Close()
}

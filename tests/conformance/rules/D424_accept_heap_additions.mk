// verdict: ok
// rule: D-424
// note: HeapText.Set and Clear, Map.Replace, TextMap.Replace, FreeEach and FreeEmpty, and Chan.FreeEmpty (a Box gives a linear value back with Take, as D441_accept_alternatives shows)
Text(a: &Alloc): OutOfMemory!int {
    var t := HeapText.New(a)
    onerr t.Free(a)
    try t.Set(a, "first")
    try t.Set(a, "second")
    n := t.Len()
    t.Clear()
    t.Free(a)
    return n
}

Maps(m: mut Map[str, int], a: &Alloc into tm, tm: mut TextMap[int]): OutOfMemory!int {
    old := try m.Replace("k", 1)
    prev := try tm.Replace(a, "k", 2)
    return (old else { 0 }) + (prev else { 0 })
}

Release(a: &Alloc, tm: TextMap[Res], te: TextMap[Res], c: Chan[Res]) {
    tm.FreeEach(a, fn(r: Res) {
        r.Close()
    })
    te.FreeEmpty(a)
    c.FreeEmpty(a)
}

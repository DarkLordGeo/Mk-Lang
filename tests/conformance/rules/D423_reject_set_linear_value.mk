// verdict: reject E0701
// rule: D-423
// note: a Set that finds its key would drop a linear value that is not Free, so it is refused at the call (Replace instead)
Put(a: &Alloc, m: mut HeapMap[int, Res], r: Res): OutOfMemory!void {
    try m.Set(a, 1, r) // <- E0701
}

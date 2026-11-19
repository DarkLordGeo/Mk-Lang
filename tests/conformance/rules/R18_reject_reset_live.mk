// verdict: reject E0501
// rule: R18
// note: ends self exempts only loans whose every live holder is self; s is live
Stale(h: &Heap): OutOfMemory!void {
    var a := try Arena.New(h)
    onerr a.Free()
    s := try a.Dup("x")
    a.Reset() // <- E0501
    Use(&s)
    a.Free()
}

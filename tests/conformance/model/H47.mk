// verdict: reject E0501
// case: H47
Bad(h: &Heap): OutOfMemory!void {
    var a := try Arena.New(h)
    onerr a.Free()
    x := try a.Put(1)
    c := Cell.New(&*x)
    a.Reset() // <- E0501
    Use(&c)
    a.Free()
}

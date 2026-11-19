// verdict: reject E0503
// case: X.Chan
Bad(h: &Heap): OutOfMemory!void {
    a := try Arena.New(h)
    onerr a.Free()
    c := try Chan[int].New(a.Alloc(), 1)
    a.Free() // <- E0503
    Use(&c)
}

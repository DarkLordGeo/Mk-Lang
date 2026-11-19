// verdict: reject E0503
// case: H51
// note: `Box.New` keeps `from a, x`, so `b` holds the arena's loan
Bad(h: &Heap): OutOfMemory!void {
    a := try Arena.New(h)
    onerr a.Free()
    b := try Box[int].New(a.Alloc(), 1)
    a.Free() // <- E0503
    n := b.Take(h.Alloc())
    Use(&n)
}

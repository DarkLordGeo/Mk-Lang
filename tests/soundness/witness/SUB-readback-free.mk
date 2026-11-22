// verdict: reject E0503
// note: twin of SUB-readback-ok: a value read back out of the sub-arena still borrows it, so freeing the sub-arena while the value is live is refused. Accepted, the read would reach freed memory
Run(a: &Arena): i64 {
    var v: i64 = 1
    var b := a.Sub() else {
        return 0
    }
    var w := b.Put(Cell.New(&v)) else {
        b.Free()
        return 0
    }
    var c := Cell.New(&v)
    c.Store(w.Load())
    y := w.Load()
    b.Free() // <- E0503
    return *y + *c.Load()
}

export Main(sys: &System) {
    _ = Run(sys.arena)
}

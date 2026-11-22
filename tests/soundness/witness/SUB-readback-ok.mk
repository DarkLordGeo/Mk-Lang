// verdict: ok
// note: a value read back out of a sub-arena's memory borrows the sub-arena, not the loans it holds by its making: storing it into a later local or back into the sub-arena's memory does not make the sub-arena's parent a store target, so the sub-arena and the frame locals it views are not stored into the parent. Rejected, one store of a read-back value refused the earlier Put too (E0505 into `*a`); the rejecting twins are SUB-readback-parent and SUB-readback-free
ToLocal(a: &Arena): i64 {
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
    h := *c.Load()
    b.Free()
    return h
}

ThroughLocal(a: &Arena): i64 {
    var v: i64 = 1
    var b := a.Sub() else {
        return 0
    }
    var w := b.Put(Cell.New(&v)) else {
        b.Free()
        return 0
    }
    var c := Cell.New(&v)
    var y: &i64 = &v
    y = w.Load()
    c.Store(y)
    h := *c.Load()
    b.Free()
    return h
}

BackInto(a: &Arena): i64 {
    var v: i64 = 66
    var b := a.Sub() else {
        return 0
    }
    var w := b.Put(Cell.New(&v)) else {
        b.Free()
        return 0
    }
    w.Store(w.Load())
    h := *w.Load()
    b.Free()
    return h
}

export Main(sys: &System) {
    _ = ToLocal(sys.arena) + ThroughLocal(sys.arena) + BackInto(sys.arena)
}

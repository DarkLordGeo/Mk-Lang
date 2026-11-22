// verdict: reject E0505
// note: twin of SUB-readback-ok: a value read back out of the sub-arena holds the frame local it views, and storing it into a pool in the parent's memory stores that local into memory that outlives it; so does storing the sub-arena's own handle there. Accepted, the parent would keep pointers to a dead frame and a freed arena
import std.mem

ReadBack(a: &Arena): i64 {
    var v: i64 = 66
    var b := a.Sub() else {
        return 0
    }
    var w := b.Put(Cell.New(&v)) else {
        b.Free()
        return 0
    }
    var pl := mem.Pool[&i64].New(a)
    g := pl.Add(w.Load()) else { // <- E0505
        b.Free()
        return 0
    }
    _ = g
    h := *w.Load()
    b.Free()
    return h
}

Handle(a: &Arena): i64 {
    var b := a.Sub() else {
        return 0
    }
    var pl := mem.Pool[&Arena].New(a)
    g := pl.Add(&b) else { // <- E0505
        b.Free()
        return 0
    }
    _ = g
    b.Free()
    return 1
}

export Main(sys: &System) {
    _ = ReadBack(sys.arena) + Handle(sys.arena)
}

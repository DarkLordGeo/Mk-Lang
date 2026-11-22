// verdict: reject E0604
// note: twin of ARN-param-memself-ok: what another parameter's memory holds, stored into a's memory, is a store of `*b` into a, which the signature must declare. Accepted, a caller would let b's arena end while a still points into it
UseA(a: &Arena, b: &Arena): i64 {
    var x := b.Put(i64(5)) else {
        return 0
    }
    var w := a.Put(Cell.New(&*x)) else { // <- E0604
        return 0
    }
    return *w.Load()
}

export Main(sys: &System) {
    _ = UseA(sys.arena, sys.arena)
}

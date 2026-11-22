// verdict: reject E0505
// note: twin of ARN-param-memself-ok: a frame local stored into the memory of a parameter's arena outlives the call there. Accepted, the caller's arena would point at a dead frame
UseA(a: &Arena): i64 {
    var v: i64 = 5
    var w := a.Put(Cell.New(&v)) else { // <- E0505
        return 0
    }
    w.Store(w.Load()) // <- E0505
    return *w.Load()
}

export Main(sys: &System) {
    _ = UseA(sys.arena)
}

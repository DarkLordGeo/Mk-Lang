// verdict: ok
// note: a value read back out of the memory of the arena a parameter `a: &Arena` refers to, stored back into that memory: a store into the arena's memory drops the loans the arena holds by being itself, so `a` need not be declared `into a`. Rejected, it was E0604 "write `*a into a`"; the rejecting twin is ARN-param-memself-other
UseA(a: &Arena): i64 {
    var x := a.Put(i64(5)) else {
        return 0
    }
    var w := a.Put(Cell.New(&*x)) else {
        return 0
    }
    w.Store(w.Load())
    return *w.Load()
}

export Main(sys: &System) {
    _ = UseA(sys.arena)
}

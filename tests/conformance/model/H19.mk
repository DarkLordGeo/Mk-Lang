// verdict: reject E0505
// case: H19
Caller() {
    long := 1
    c := Cell.New(&long)
    alias := &c
    var out := &long
    if Cond() {
        short := 2
        c.Store(&short)
        out = alias.Load() // <- E0505
    }
    Use(out)
}

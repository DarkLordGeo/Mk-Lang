// verdict: reject E0505
// case: R6.Hole
// also: H20
Both(a: &Cell[&i32], b: &Cell[&i32], v: &i32 into a): &i32 from b {
    a.Store(v)
    return b.Load()
}

Hole() {
    long: i32 = 1
    c := Cell.New(&long)
    var out := &long
    if Cond() {
        short: i32 = 2
        out = Both(&c, &c, &short) // <- E0505
    }
    Use(out)
}

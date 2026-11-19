// verdict: reject E0505
// case: R11.Store
// also: H46
Store(c: &Cell[?&i32], p: &Point into c) {
    c.Store(some(&p.x))
}

Leak() {
    cell := Cell[?&i32].New(none)
    if Cond() {
        pt := Point(x: 1, y: 2)
        Store(&cell, &pt) // <- E0505
    }
    if r := cell.Load() {
        Use(r)
    }
}

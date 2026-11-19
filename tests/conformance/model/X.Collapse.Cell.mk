// verdict: reject E0604
// case: X.Collapse.Cell
// note: r points at a dead local. The model left E0604 unmarked; the checker puts it at the store
struct D2 {
    a: &i32
    c: &Cell[&i32]
}

D2.Mix(&self) {
    self.c.Store(self.a) // <- E0604
}

Caller() {
    long: i32 = 1
    c := Cell.New(&long)
    if Cond() {
        short: i32 = 2
        d := D2(a: &short, c: &c)
        d.Mix()
    }
    r := c.Load()
    Use(r)
}

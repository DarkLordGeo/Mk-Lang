// verdict: ok
// rule: D-654
// rule: R11
// note: the callee half: under `into self`, a body may store through a `Cell` that `self` reaches by a shared reference field, from `mut self` and from `&self`; the caller counts that cell (D654_reject_store_through_picked_cell)
struct H {
    c: &Cell[&i32]
    n: int
}

H.Put(mut self, x: &i32 into self) {
    self.c.Store(x)
}

H.Put2(&self, x: &i32 into self) {
    self.c.Store(x)
}

Keep(outer: &Cell[&i32], x: &i32 into outer) {
    var h := H(c: outer, n: 0)
    h.Put(x)
    h.Put2(x)
}

// verdict: reject E0604
// rule: R11
// note: a receiver's store needs its clause as a parameter's does: without `self into c`, storing self's reference into c is E0604, and the fix writes the clause
struct Ref {
    p: &i32
}

Ref.Put(self, c: &Cell[?&i32]) {
    c.Store(some(self.p)) // <- E0604
}

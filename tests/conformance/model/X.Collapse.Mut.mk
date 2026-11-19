// verdict: reject E0604
// case: X.Collapse.Mut
// note: Use(p) reads a dead local. The model left E0604 unmarked; the checker puts it at the store
struct Deep {
    a: &i32
    b: mut &i32
}

Deep.Mix(mut self) {
    *self.b = self.a // <- E0604
}

Caller() {
    long: i32 = 1
    var p := &long
    if Cond() {
        short: i32 = 2
        var d := Deep(a: &short, b: mut p)
        d.Mix()
    }
    Use(p)
}

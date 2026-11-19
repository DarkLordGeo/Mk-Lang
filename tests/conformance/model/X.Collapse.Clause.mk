// verdict: reject E0504 E0505
// case: X.Collapse.Clause
// note: the store `*self into self` is declared, and through d.b it reaches p. E0504 is the cost: d held `mut p`, so p now holds a loan on itself. E0505 is reported at the `d.Mix()` line, the statement that stores the loan on `short` made by `Deep(...)` into p
struct Deep {
    a: &i32
    b: mut &i32
}

Deep.Mix(mut self) *self into self {
    *self.b = self.a
}

Caller() {
    long: i32 = 1
    var p := &long
    if Cond() {
        short: i32 = 2
        var d := Deep(a: &short, b: mut p)
        d.Mix() // <- E0505
    }
    Use(p) // <- E0504
}

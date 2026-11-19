// verdict: ok
// note: a record result takes a per-field clause as a struct does, by its canonical field order; taken apart at once, p holds the loan on x alone
F(a: &i32, b: &i32): (p: &i32, q: &i32) from (p: a, q: b) {
    return (p: a, q: b)
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        (p, q) := F(&x, &y)
        Use(q)
        keep = p
    }
    Use(keep)
}

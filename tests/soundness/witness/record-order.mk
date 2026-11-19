// verdict: ok
// note: a record's fields are indexed in canonical (name) order, here written q before p; the clause, the literal and the destructuring agree on it, so p holds the loan on x alone
F(a: &i32, b: &i32): (q: &i32, p: &i32) from (q: b, p: a) {
    return (q: b, p: a)
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        (q, p) := F(&x, &y)
        Use(q)
        keep = p
    }
    Use(keep)
}

// verdict: ok
// note: a record literal written out of canonical order into a temporary splits by the canonical order, so p holds the loan on x alone, with no clause anywhere
Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        (q, p) := (q: &y, p: &x)
        Use(q)
        keep = p
    }
    Use(keep)
}

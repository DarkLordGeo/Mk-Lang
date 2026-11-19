// verdict: reject E0602
// note: a literal returned under a per-field clause is checked field by field. Crossed, field r holds the loan of b, which only s may hold. Without the check the caller's r would hold the loan on y unseen (M-F8-W2-ret-union)
struct Pair {
    r: &i32
    s: &i32
}

F(a: &i32, b: &i32): Pair from (r: a, s: b) {
    return Pair(r: b, s: a) // <- E0602
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Pair(r, s) := F(&x, &y)
        Use(s)
        keep = r
    }
    Use(keep)
}

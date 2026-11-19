// verdict: reject E0602
// note: a field the clause leaves out holds every source the clause names, here a alone, so it is checked against a: s holding the loan of b is E0602, and the caller's s holds the loan on x only (M-F8-W3-unnamed-free)
struct Pair {
    r: &i32
    s: &i32
}

F(a: &i32, b: &i32): Pair from (r: a) {
    return Pair(r: a, s: b) // <- E0602
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Pair(r, s) := F(&x, &y)
        Use(r)
        keep = s
    }
    Use(keep)
}

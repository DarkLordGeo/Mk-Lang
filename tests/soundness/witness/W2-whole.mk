// verdict: reject E0602
// note: a value with one set (here the result of a flat clause) returned under a per-field clause must be covered by the sources of every named field; b is no source of r (M-F8-whole-union)
struct Pair {
    r: &i32
    s: &i32
}

Flat(a: &i32, b: &i32): Pair from a, b {
    return Pair(r: b, s: a)
}

F(a: &i32, b: &i32): Pair from (r: a, s: b) {
    return Flat(a, b) // <- E0602
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

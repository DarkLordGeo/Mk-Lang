// verdict: reject E0602
// note: a fn literal under a per-field fn type is checked field by field at its return; crossed, r would hold the loan on b, and Apply's keep would outlive y (M-F8-W2-ret-union)
struct Pair {
    r: &i32
    s: &i32
}

Apply(g: fn(a: &i32, b: &i32): Pair from (r: a, s: b), x: &i32) {
    var keep := x
    if Cond() {
        y: i32 = 2
        Pair(r, s) := g(x, &y)
        Use(s)
        keep = r
    }
    Use(keep)
}

Caller() {
    x: i32 = 1
    Apply(fn(a: &i32, b: &i32): Pair { return Pair(r: b, s: a) }, &x) // <- E0602
}

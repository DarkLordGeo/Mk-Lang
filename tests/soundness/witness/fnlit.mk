// verdict: ok
// note: a fn literal given for a per-field fn type takes the type's clause, field by field, and its body is checked against it; this literal builds exactly the declared fields, so it fits, and the caller's split gives r the loan on x alone
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
    Apply(fn(a: &i32, b: &i32): Pair { return Pair(r: a, s: b) }, &x)
}

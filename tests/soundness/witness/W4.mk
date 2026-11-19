// verdict: reject E0608
// note: a whole-clause function given where a per-field fn type is expected must be covered in every field (R9); the r of G may hold b, which the r of the fn type does not allow, so Apply would read the loan on y through r. Under M-F8-W4-cover-flat this is an internal error, not an acceptance: mir.Validate reads types.FlowsCovered itself (W4-iface.mk is the mutant's witness)
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

G(a: &i32, b: &i32): Pair from a, b {
    return Pair(r: b, s: a)
}

Caller() {
    x: i32 = 1
    Apply(G, &x) // <- E0608
}

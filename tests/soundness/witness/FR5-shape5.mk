// verdict: ok
// note: field-level loan sets, shape 5: `p := try H() else` binds the payload (_t as some).0 whole to a named local. A local bound whole from a per-field call starts split, so p.r holds only x's loan; with one set for the local, p.r would keep the loan on y (E0505 at `keep = p.r`) (H09's family)
struct Pair {
    r: &i32
    s: &i32
}

error Fail

H(a: &i32, b: &i32): Fail!?Pair from (r: a, s: b) {
    return some(Pair(r: a, s: b))
}

Caller(): Fail!void {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        p := try H(&x, &y) else {
            return
        }
        keep = p.r
    }
    Use(keep)
}

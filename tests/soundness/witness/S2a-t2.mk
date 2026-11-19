// verdict: reject E0505
// note: `Mix(mut t)`, whose clauses name no store into t: Mix needs no clause for `t.a = t.b` (R11's first-level exemption), and the call makes t whole, so t.a holds y's loan: E0505 at y's borrow, as in S2a-clause.mk. The mutant M-RV2-1-excl-collapse (an exclusive whole borrow keeps the root split) accepts it, and Use(t.a) would then read y after its scope
struct Two {
    a: &i32
    b: &i32
}

Mix(t: mut Two) {
    t.a = t.b
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        Mix(mut t)
    }
    Use(t.a)
}

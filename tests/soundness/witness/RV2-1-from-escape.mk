// verdict: reject E0505
// note: `r := Pick(mut t)` with `Pick(x: mut Two): mut Two from x` must not keep t split while r holds a whole loan on t, and r goes to the generic Gen. Every exclusive whole borrow of a split local collapses it, whatever the callee, so t holds y's loan whole and y's borrow is E0505. M-RV2-1-excl-collapse alone does not flip it: the body also has a call that may take part in a self-reach through a forwarding parameter, so the whole-body fallback rejects it too
struct Two {
    a: &i32
    b: &i32
}

Gen[T](x: mut T) {
}

Pick(x: mut Two): mut Two from x {
    return x
}

Caller() {
    long: i32 = 1
    var t := Two(a: &long, b: &long)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        r := Pick(mut t)
        Gen(r)
    }
    Use(t.a)
}

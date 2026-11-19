// verdict: reject E0505
// note: a whole use (R6): a use of the whole of t uses every field, so `Use(&t)` keeps t.b's loan on y live (mutant M-F8-S2-whole-use-one: a whole use keeps field 0 live)
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
    }
    Use(&t)
}

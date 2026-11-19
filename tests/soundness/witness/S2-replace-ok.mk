// verdict: ok
// note: Replace, precision: y's set lands in field a only, so t.b may be read after y ends
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        old := Replace(mut t.a, &y)
        Use(old)
        Use(t.a)
    }
    Use(t.b)
}

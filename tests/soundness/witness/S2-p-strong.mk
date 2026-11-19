// verdict: ok
// note: precision control: `t.b = &x` is a strong update, so y's loan leaves t.b
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        t.b = &y
        Use(t.b)
        t.b = &x
    }
    Use(t.b)
    Use(t.a)
}

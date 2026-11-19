// verdict: ok
// note: flows8 precision control P.join: the field-wise join keeps y in t.b only
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        if Cond() {
            t.b = &y
        } else {
            t.b = &y
        }
        Use(t.b)
    }
    Use(t.a)
}

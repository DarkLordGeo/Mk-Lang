// verdict: ok
// note: flows8 precision control P.destruct (A5): a destructuring gives each binding its field's set
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        t := Two(a: &x, b: &y)
        Two(a, b) := t
        Use(b)
        keep = a
    }
    Use(keep)
}

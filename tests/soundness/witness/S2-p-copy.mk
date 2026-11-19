// verdict: ok
// note: flows8 precision control P.copy (A5): a whole copy keeps each field's set
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
        u := t
        Use(u.b)
    }
    Use(t.a)
}

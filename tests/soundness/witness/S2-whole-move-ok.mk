// verdict: ok
// note: precision: after the whole move and the whole assignment, h.b holds x's loan only, and the moved value's field a holds x's loan only
struct Hold {
    a: &i32
    b: &i32
    m: mut i32
}

Caller() {
    x: i32 = 1
    var z: i32 = 0
    var w: i32 = 0
    var h := Hold(a: &x, b: &x, m: mut z)
    var keep := &x
    if Cond() {
        y: i32 = 2
        h.b = &y
        u := h
        keep = u.a
        h = Hold(a: &x, b: &x, m: mut w)
    }
    Use(keep)
    Use(h.b)
}

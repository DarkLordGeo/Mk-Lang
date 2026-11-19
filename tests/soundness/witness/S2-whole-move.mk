// verdict: reject E0505
// note: a whole move leaves h moved, its mode kept and its sets empty, and a whole assignment splits it again; u, the moved value, holds h's fields
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
        keep = u.b // <- E0505
        h = Hold(a: &x, b: &x, m: mut w)
    }
    Use(keep)
    Use(h.b)
}

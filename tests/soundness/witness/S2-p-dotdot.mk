// verdict: ok
// note: the `..` pattern: `Two(a, ..) := t` gives a its field's set
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
        Two(a, ..) := t
        Use(t.b)
        keep = a
    }
    Use(keep)
}

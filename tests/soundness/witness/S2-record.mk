// verdict: ok
// note: a record local splits as a struct does
Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        var t := (a: &x, b: &x)
        t.b = &y
        Use(t.b)
        keep = t.a
    }
    Use(keep)
}

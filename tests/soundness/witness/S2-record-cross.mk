// verdict: reject E0505
// note: a record local's field write lands in its field
Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        var t := (a: &x, b: &x)
        t.b = &y
        keep = t.b // <- E0505
    }
    Use(keep)
}

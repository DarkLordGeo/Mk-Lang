// verdict: reject E0505
// note: `Swap(mut t.a, mut t.b)` leaves both fields with the union of the two, so t.a holds y
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        Swap(mut t.a, mut t.b)
    }
    Use(t.a)
}

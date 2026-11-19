// verdict: reject E0505
// note: a mut borrow of the whole kept in a local makes t whole, so a store through it reaches t.a
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        m := mut t
        m.b = &y // <- E0505
    }
    Use(t.a)
}

// verdict: reject E0505
// note: a clause-free `Pick(mut t)` makes t whole, and its result r holds the whole exclusive loan on t; a store through r lands in every field, so t.b holds y
struct Two {
    a: &i32
    b: &i32
}

Pick(t: mut Two): mut &i32 {
    return mut t.a
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        r := Pick(mut t)
        *r = &y // <- E0505
    }
    Use(t.b)
}

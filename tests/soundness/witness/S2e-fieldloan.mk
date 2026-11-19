// verdict: reject E0505
// note: a store through a loan on t.a lands in field a, so t.a holds y after `*r = &y`
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        r := mut t.a
        *r = &y // <- E0505
    }
    Use(t.a)
}

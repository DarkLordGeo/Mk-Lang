// verdict: reject E0505
// note: `Replace(mut t.a, &y)` adds y's set to field a
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        old := Replace(mut t.a, &y) // <- E0505
        Use(old)
    }
    Use(t.a)
}

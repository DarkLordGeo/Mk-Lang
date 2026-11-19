// verdict: reject E0505
// note: `Mix(mut t)` makes t whole, so `keep = t.a` takes the union and holds y's loan: E0505 at the statement that stores it. If t stayed split, Mix could move a loan between fields with no clause (R11's first-level exemption) while the caller's t.a held x's loan only
struct Two {
    a: &i32
    b: &i32
}

Mix(t: mut Two) {
    t.a = t.b
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        var t := Two(a: &x, b: &y)
        Mix(mut t)
        keep = t.a // <- E0505
    }
    Use(keep)
}

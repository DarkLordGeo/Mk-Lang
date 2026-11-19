// verdict: reject E0505
// note: a join takes each field's union (R7), so the arm that wrote t.a keeps y in it; a join that meets field sets loses it (mutant M-F8-S2d-join-meet)
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        if Cond() {
            t.a = &y // <- E0505
        } else {
            t.b = &y
        }
    }
    Use(t.a)
}

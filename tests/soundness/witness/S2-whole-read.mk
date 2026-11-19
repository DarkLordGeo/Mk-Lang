// verdict: reject E0505
// note: a whole read: a whole copy of a split t passed by value holds the union of its fields, so PickB's result holds y (mutant M-F8-S2-whole-read-one: a whole read gives field 0's set)
struct Two {
    a: &i32
    b: &i32
}

PickB(t: Two): &i32 {
    return t.b
}

Caller() {
    x: i32 = 1
    var keep := &x
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        t.b = &y
        keep = PickB(t) // <- E0505
    }
    Use(keep)
}

// verdict: reject E0505
// note: a shared borrow of the whole keeps the split: the shared loan reaches the union of the fields, so SeeT's result holds y
struct Two {
    a: &i32
    b: &i32
}

SeeT(t: &Two): &i32 {
    return t.b
}

Caller() {
    x: i32 = 1
    var keep := &x
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        t.b = &y
        keep = SeeT(&t) // <- E0505
    }
    Use(keep)
}

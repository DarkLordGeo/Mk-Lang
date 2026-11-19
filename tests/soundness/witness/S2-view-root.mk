// verdict: ok
// note: a view root splits as a struct does, so `v.b = &y` lands in field b and its rest, and Use(v.a) reads field a alone, which holds only x; nothing reads the rest after y ends. P4-view-rest-u1 is the raw-storage twin that ends in a whole use and is rejected
view struct VTwo {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var v := VTwo(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        v.b = &y
    }
    Use(v.a)
}

// verdict: reject E0502
// rule: R8
struct Holder {
    name: &i32
}

Get(h: &Holder): &i32 {
    return h.name
}

Keep() {
    var n: i32 = 1
    h := Holder(name: &n)
    r := Get(&h)
    n = 2 // <- E0502
    Use(r)
}

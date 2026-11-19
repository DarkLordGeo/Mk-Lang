// verdict: ok
// note: a shared `&t` of a type not writable by type keeps t split; the loan reaches the union
struct Two {
    a: &i32
    b: &i32
}

SeeT(t: &Two): &i32 {
    return t.b
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        t.b = &y
        Use(SeeT(&t))
    }
    Use(t.a)
}

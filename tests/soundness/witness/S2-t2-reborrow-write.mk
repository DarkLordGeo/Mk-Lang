// verdict: reject E0505
// note: `x := mut p.a; x.b = v` writes through a reborrow local, so the destination field is unknown and the store moves a loan across fields. F needs no clause for it (R11's first-level exemption), and `F(mut t)` makes t whole, as every exclusive whole borrow does, so t holds y's loan: E0505 at y's borrow
struct Q {
    c: &i32
    b: &i32
}

struct P {
    a: Q
    b: &i32
}

F(p: mut P) {
    v := p.b
    x := mut p.a
    x.b = v
}

Caller() {
    x0: i32 = 0
    var t := P(a: Q(c: &x0, b: &x0), b: &x0)
    if Cond() {
        y: i32 = 1
        t.b = &y // <- E0505
        F(mut t)
    }
    Use(t.a.b)
}

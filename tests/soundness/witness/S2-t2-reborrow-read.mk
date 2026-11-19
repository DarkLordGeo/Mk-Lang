// verdict: reject E0505
// note: `x := &p.a; v := x.b` reads through a reborrow local, so v's field is unknown and `p.b = v` moves a loan across fields. F needs no clause for it (R11's first-level exemption), and `F(mut t)` makes t whole, as every exclusive whole borrow does, so t holds y's loan: E0505 at y's borrow. Judging the field relative to the place (Q.b and P.b are both field 1) and keeping t split would let the caller read y after its scope
struct Q {
    c: &i32
    b: &i32
}

struct P {
    a: Q
    b: &i32
}

F(p: mut P) {
    x := &p.a
    v := x.b
    p.b = v
}

Caller() {
    x0: i32 = 0
    var t := P(a: Q(c: &x0, b: &x0), b: &x0)
    if Cond() {
        y: i32 = 1
        t.a.b = &y // <- E0505
        F(mut t)
    }
    Use(t.b)
}

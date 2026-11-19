// verdict: reject E0505
// note: a store that stays in the field it was read from, both rooted at p, needs no clause, but `F(mut t)` still makes t whole, as every exclusive whole borrow does, so t holds y's loan: E0505 at y's borrow. Keeping t split here is precision the checker gives up
struct Q {
    c: &i32
    b: &i32
}

struct P {
    a: Q
    b: &i32
}

F(p: mut P) {
    v := p.a.b
    p.a.c = v
    w := p.b
    p.b = w
}

Caller() {
    x0: i32 = 0
    var t := P(a: Q(c: &x0, b: &x0), b: &x0)
    if Cond() {
        y: i32 = 1
        t.b = &y // <- E0505
        F(mut t)
        Use(t.b)
    }
    Use(t.a.c)
}

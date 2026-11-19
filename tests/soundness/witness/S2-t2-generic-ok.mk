// verdict: reject E0505
// note: a clause-free callee of the same struct: `Look(mut t)` makes t whole, as every exclusive whole borrow does, so t.a holds y's loan: E0505 at y's borrow. Keeping t split would accept this program, but a callee may move loans between fields with no clause, so the checker gives up that precision
struct Two {
    a: &i32
    b: &i32
}

Look(t: mut Two) {
    t.b = t.b
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        Look(mut t)
    }
    Use(t.a)
}

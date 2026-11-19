// verdict: reject E0505
// note: with the clause `*t into t` the call makes t whole, as it does without one, and the `*t into t` store puts the union into every field, so this file is rejected too; S2a-t2.mk is the clause-free twin
struct Two {
    a: &i32
    b: &i32
}

Mix(t: mut Two) *t into t {
    t.a = t.b
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        Mix(mut t)
    }
    Use(t.a)
}

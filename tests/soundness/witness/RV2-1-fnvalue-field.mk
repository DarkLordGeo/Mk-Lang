// verdict: reject E0505
// note: the extern reached through a `fn` field of a struct passed `&`: the checker collapses t at the call, so t holds y's loan whole and y's borrow is E0505. Mutant M-RV2-1-excl-collapse accepts it
struct Two {
    a: &i32
    b: &i32
}

struct Ops {
    f: fn(mut Two)
}

extern Mix(t: mut Two)

Outer(t: mut Two, o: &Ops) {
    o.f(t)
}

Caller() {
    long: i32 = 1
    var t := Two(a: &long, b: &long)
    var o: Ops = Ops(f: Outer2)
    unsafe "D-587: Mix is named as a value" {
        o = Ops(f: Mix)
    }
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        Outer(mut t, &o)
    }
    Use(t.a)
}

Outer2(t: mut Two) {
}

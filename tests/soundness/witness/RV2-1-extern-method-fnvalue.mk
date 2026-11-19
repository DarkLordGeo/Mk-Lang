// verdict: reject E0505
// note: the extern reached through a `mut self` method, `t.Run(Mix)`: the checker collapses t at the call, so t holds y's loan whole and y's borrow is E0505. Mutant M-RV2-1-excl-collapse accepts it
struct Two {
    a: &i32
    b: &i32
}

extern Mix(t: mut Two)

Two.Run(mut self, f: fn(mut Two)) {
    f(self)
}

Caller() {
    long: i32 = 1
    var t := Two(a: &long, b: &long)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        unsafe "D-587: Mix is named as a value" {
            t.Run(Mix)
        }
    }
    Use(t.a)
}

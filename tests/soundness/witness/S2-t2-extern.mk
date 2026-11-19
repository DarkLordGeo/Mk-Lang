// verdict: reject E0505
// note: an extern callee: Mix's signature is not checked against a body (its body is C), and `Mix(mut t)` makes t whole, as every exclusive whole borrow does; t then holds y after the `if`, and `t.b = &y` is E0505. If t stayed split and Mix moved t.b into t.a, Use(t.a) would read y after its scope; M-RV2-1-excl-collapse flips this file. A witness is judged in an unsafe module, so its unsafe needs no pkg.mk
struct Two {
    a: &i32
    b: &i32
}

extern Mix(t: mut Two)

Caller() {
    long: i32 = 1
    var t := Two(a: &long, b: &long)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        unsafe "Mix is C" {
            Mix(mut t)
        }
    }
    Use(t.a)
}

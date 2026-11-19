// verdict: reject E0505
// note: the extern Mix converts to `fn(mut Two)`, and the caller of f cannot see which function it holds; `f(mut t)` makes t whole, as every exclusive whole borrow does, so `t.b = &y` is E0505. If t stayed split and Mix moved t.b into t.a, Use(t.a) would read y after its scope; M-RV2-1-excl-collapse flips this file. Naming the extern as a value outside `unsafe` is E1101, a check-phase error that would hide every flow diagnostic, so the naming sits inside an `unsafe` block (a witness is judged in an unsafe module)
struct Two {
    a: &i32
    b: &i32
}

extern Mix(t: mut Two)

Caller() {
    unsafe "D-587: Mix is named as a value" {
        long: i32 = 1
        f: fn(mut Two) = Mix
        var t := Two(a: &long, b: &long)
        if Cond() {
            y: i32 = 2
            t.b = &y // <- E0505
            f(mut t)
        }
        Use(t.a)
    }
}

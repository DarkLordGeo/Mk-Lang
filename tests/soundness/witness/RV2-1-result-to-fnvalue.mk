// verdict: reject E0505
// note: `r := Pick(mut t)`, and r goes to an extern through a fn value; if t stayed split, `t.a` would read y after its scope. Every exclusive whole borrow of a split local collapses it, whatever the callee, so t holds y's loan whole and y's borrow is E0505. M-RV2-1-excl-collapse alone does not flip it: the body also has a call that may take part in a self-reach through a forwarding parameter, so the whole-body fallback rejects it too. Naming the extern as a value outside `unsafe` is E1101, a check-phase error that hides every flow diagnostic, so the naming sits inside an `unsafe` block (a witness is judged in an unsafe module); the borrow program is unchanged
struct Two {
    a: &i32
    b: &i32
}

extern Mix(t: mut Two)

Pick(t: mut Two): mut Two from t {
    return t
}

Caller() {
    unsafe "D-587: Mix is named as a value" {
        long: i32 = 1
        f: fn(mut Two) = Mix
        var t := Two(a: &long, b: &long)
        if Cond() {
            y: i32 = 2
            t.b = &y // <- E0505
            r := Pick(mut t)
            f(r)
        }
        Use(t.a)
    }
}

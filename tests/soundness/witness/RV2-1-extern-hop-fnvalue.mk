// verdict: reject E0505
// note: `Outer(t: mut Two, f: fn(mut Two)) { f(t) }` hands t to an extern through a fn value; with a C body `t->a = t->b`, `t.a` would read y after its scope. Every exclusive whole borrow of a split local collapses it, whatever the callee, so t holds y's loan whole and y's borrow is E0505. Mutant M-RV2-1-excl-collapse (an exclusive whole borrow keeps the root split) accepts it. Naming the extern as a value outside `unsafe` is E1101, a check-phase error that hides every flow diagnostic, so the naming sits inside an `unsafe` block (a witness is judged in an unsafe module); the borrow program is unchanged
struct Two {
    a: &i32
    b: &i32
}

extern Mix(t: mut Two)

Outer(t: mut Two, f: fn(mut Two)) {
    f(t)
}

Caller() {
    long: i32 = 1
    var t := Two(a: &long, b: &long)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        unsafe "D-587: Mix is named as a value" {
            Outer(mut t, Mix)
        }
    }
    Use(t.a)
}

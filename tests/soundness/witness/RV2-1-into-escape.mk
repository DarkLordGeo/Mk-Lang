// verdict: reject E0505
// note: `Stash(t: mut Two into h, h: mut Hold)` stores the whole reference t into h, and `TakeHold(mut h)` then hands *t to the generic Gen. Every exclusive whole borrow of a split local collapses it, whatever the callee, so t holds y's loan whole and y's borrow is E0505. Mutant M-RV2-1-excl-collapse (an exclusive whole borrow keeps the root split) accepts it
struct Two {
    a: &i32
    b: &i32
}

Gen[T](x: mut T) {
}

struct Hold {
    r: ?mut Two
}

Stash(t: mut Two into h, h: mut Hold) {
    h.r = some(t)
}

TakeHold(h: mut Hold) {
    if r := mut h.r {
        Gen(mut **r)
    }
}

Caller() {
    long: i32 = 1
    var t := Two(a: &long, b: &long)
    if Cond() {
        y: i32 = 2
        t.b = &y // <- E0505
        var h := Hold(r: none)
        Stash(mut t, mut h)
        TakeHold(mut h)
    }
    Use(t.a)
}

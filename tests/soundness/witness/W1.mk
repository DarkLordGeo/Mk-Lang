// verdict: reject E0505
// note: a per-field call bound to a var local whose fields are written before a destructuring: `o.f.h = &y` adds y to field f's set, which the following `o.f.g = &x` must not drop (a write inside a field is a weak update), and the join keeps each field's union, so f.h holds y and `keep = f.h` is E0505. Two mutants flip this file: M-F8-S2c-strong-deep and M-F8-S2d-join-meet
struct Inner {
    g: &i32
    h: &i32
}

struct Outer {
    f: Inner
    k: &i32
}

MkO(a: &i32, b: &i32): Outer from (f: a, k: b) {
    return Outer(f: Inner(g: a, h: a), k: b)
}

Caller() {
    x: i32 = 1
    var keep := &x
    var o := MkO(&x, &x)
    if Cond() {
        y: i32 = 2
        if Cond() {
            o.f.h = &y
            o.f.g = &x
        } else {
            o.k = &y
        }
        Outer(f, k) := o
        Use(k)
        keep = f.h // <- E0505
    }
    Use(keep)
}

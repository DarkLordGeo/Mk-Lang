// verdict: reject E0505
// note: `o.f.h = &y` writes inside field f, so it adds to f's set (a weak update); a strong update of f would drop y's loan when `o.f.g = &x` follows (mutant M-F8-S2c-strong-deep)
struct Inner {
    g: &i32
    h: &i32
}

struct Outer {
    f: Inner
    k: &i32
}

Caller() {
    x: i32 = 1
    var o := Outer(f: Inner(g: &x, h: &x), k: &x)
    if Cond() {
        y: i32 = 2
        o.f.h = &y // <- E0505
        o.f.g = &x
    }
    Use(o.f.h)
}

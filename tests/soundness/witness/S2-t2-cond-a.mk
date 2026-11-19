// verdict: ok
// note: a store into *p whole, `*p = Two(a: p.b, b: p.a)`, is a store into the first level of the callee's own parameter, which R11 exempts, so the program is clean
struct Two {
    a: &i32
    b: &i32
}

F(p: mut Two) {
    *p = Two(a: p.b, b: p.a)
}

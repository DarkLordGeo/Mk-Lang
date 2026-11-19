// verdict: ok
// note: `Put(mut p.b, p.a.b)` (`v into x` stores v into p's first level) needs no `*p into p` though the loan comes from inside p: R11's first-level exemption covers the callee's store, so the program is clean
struct Q {
    c: &i32
    b: &i32
}

struct P {
    a: Q
    b: &i32
}

Put(x: mut &i32, v: &i32 into x) {
    *x = v
}

F(p: mut P) {
    Put(mut p.b, p.a.b)
}

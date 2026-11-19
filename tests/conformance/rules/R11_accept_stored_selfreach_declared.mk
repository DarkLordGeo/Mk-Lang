// verdict: ok
// rule: R11
// note: the self-store of R11_reject_stored_selfreach is clean when declared: `x: S into x` writes one of x's loans through another, a forwarder restates it, and a function that stores x into h without writing through it needs only `x into h`
struct S {
    a: mut &i32
    b: &i32
}

struct H {
    s: S
}

Mix(x: S into x) {
    *x.a = x.b
}

Forward(x: S into x) {
    Mix(x)
}

Keep(h: mut H, x: S into h) {
    h.s = x
}

KeepAndRead(h: mut H, x: S into h): i32 {
    h.s = x
    return *h.s.b
}

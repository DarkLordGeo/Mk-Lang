// verdict: reject E0604
// rule: R11
// note: a parameter's node holds what the body stored into it: once `h.s = x` stores x into h, a write of x's loan through h.s.a, directly, through a call, through a result from h or after a join, is a store of x into x, which no signature here declares
struct S {
    a: mut &i32
    b: &i32
}

struct H {
    s: S
}

Direct(h: mut H, x: S into h) {
    v := x.b
    h.s = x
    *h.s.a = v // <- E0604
}

Write(h: mut H, v: &i32 into h) {
    *h.s.a = v
}

ThroughCall(h: mut H, x: S into h) {
    v := x.b
    h.s = x
    Write(h, v) // <- E0604
}

Get(h: mut H): mut S from h {
    return mut h.s
}

ThroughResult(h: mut H, x: S into h) {
    v := x.b
    h.s = x
    s := Get(h)
    *s.a = v // <- E0604
}

AfterJoin(h: mut H, x: S into h, c: bool) {
    v := x.b
    if c {
        h.s = x
    }
    *h.s.a = v // <- E0604
}

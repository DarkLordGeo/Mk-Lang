// verdict: reject E0604
// note: self-reach through a chain of clauses: K(g, h into g, t into h) writes t.b, a copy of x.b, through g.s.a, which is x.a. Outer passes its local gl, which holds x, and t, so x reaches x through the chain. A later round of the store fixpoint skips only what the call itself stored, not gl's own copy of x's loan, so Outer gets E0604, as it does at round 0 when K declares `t: T into g`. Accepted, `mk run` prints `keep=0 r=12069 k=12069`; a change of this verdict reopens the hole
struct S {
    a: mut &i32
    b: &i32
}

struct H {
    s: ?S
}

struct G {
    s: S
}

struct T {
    b: &i32
}

K(g: mut G, h: H into g, t: T into h) {
    v := t.b
    *g.s.a = v
}

Outer(x: S) {
    var gl := G(s: x)
    t := T(b: gl.s.b)
    K(mut gl, H(s: none), t) // <- E0604
}

Inner(p: mut &i32) {
    n: i32 = 7
    Outer(S(a: p, b: &n))
}

Clobber(n: i32): i32 {
    var acc: i32 = n
    var w0: i32 = n + 1001
    var w1: i32 = n + 2002
    for _ in 0..4 {
        acc = acc + w0 + w1
        w0 = w0 + 1
        w1 = w1 + 1
    }
    return acc
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    keep: i32 = 0
    var r: &i32 = &keep
    Inner(mut r)
    k := Clobber(5)
    v := *r
    try Print(mut out, "keep={keep} r={v} k={k}\n", (keep, v, k))
}

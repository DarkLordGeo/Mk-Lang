// verdict: reject E0604
// note: self-reach through a forwarding parameter: Outer destructures x and rebuilds the literal. Accepted, `mk run` prints `keep=0 r=12069 k=12069` from a dead local; a change of this verdict reopens the hole
struct S {
    a: mut &i32
    b: &i32
}

struct H {
    s: S
}

F(h: mut H, x: S into h) {
    v := x.b
    h.s = x
    *h.s.a = v // <- E0604
}

Outer(x: S) {
    dummy: i32 = 0
    var dr: &i32 = &dummy
    var h := H(s: S(a: mut dr, b: &dummy))
    S(a: pa, b: pb) := x
    F(mut h, S(a: pa, b: pb))
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

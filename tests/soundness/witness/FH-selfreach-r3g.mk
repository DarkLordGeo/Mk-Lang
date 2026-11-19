// verdict: reject E0604
// note: self-reach through a forwarding parameter: r3's F reached through a clause-less wrapper G whose parameters are references. Accepted, `mk run` prints `keep=0 r=7 k=40149` from a dead local; a change of this verdict reopens the hole
struct S {
    a: mut &i32
    b: &i32
}

struct Hd {
    s: S
}

struct TT {
    t: &i32
    s: &i32
}

F(l: mut Hd, x: S into l) {
    v := x.b
    l.s = x
    *l.s.a = v // <- E0604
}

Outer(x: S) {
    dummy: i32 = 0
    var dr: &i32 = &dummy
    var h := Hd(s: S(a: mut dr, b: &dummy))
    F(mut h, x)
}

G(r: mut &i32, b: &i32) {
    Outer(S(a: mut *r, b: b))
}

Clobber(n: i32): i32 {
    var acc: i32 = n
    var w0: i32 = n + 1001
    var w1: i32 = n + 2002
    var w2: i32 = n + 3003
    var w3: i32 = n + 4004
    for _ in 0..4 {
        acc = acc + w0 + w1 + w2 + w3
        w0 = w0 + 1
        w1 = w1 + 1
        w2 = w2 + 1
        w3 = w3 + 1
    }
    return acc
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    keep: i32 = 0
    var r: &i32 = &keep
    x0: i32 = 0
    var pp := TT(t: &x0, s: &x0)
    if sys.args.Len() >= 0 {
        n: i32 = 7
        G(mut r, &n)
        pp.t = r
    }
    k := Clobber(5)
    w := pp.t
    v := *w
    try Print(mut out, "keep={keep} r={v} k={k}\n", (keep, v, k))
}

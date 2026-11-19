// verdict: reject E0505 E0604
// note: Outer forwards x into h through F, whose clause `x: S into l` lets it store x's loans into l, and Main's call is decorated (here: join) so that one loan set per local would reject it by accident; a checker that accepts it reads the dead local n when run (r=7). A body with a call that may reach such a forwarder is checked whole (call.go remnantReach, env.go Check), and F's own check rejects its write `*l.s.a = v` as a store of x into x (E0604, marked)
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

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    keep: i32 = 0
    var r: &i32 = &keep
    x0: i32 = 0
    var pp := TT(t: &x0, s: &x0)
    if sys.args.Len() >= 0 {
        n: i32 = 7
        if sys.args.Len() >= 0 {
            pp.s = &n // <- E0505
        } else {
            pp.t = &x0
        }
        Outer(S(a: mut r, b: &n))
        pp.t = r
    }
    w := pp.t
    k := Clobber(5)
    v := *w
    try Print(mut out, "keep={keep} r={v} k={k}\n", (keep, v, k))
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

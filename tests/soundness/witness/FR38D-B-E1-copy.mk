// verdict: reject E0505 E0604
// note: self-reach through a forwarding parameter (FH-selfreach-r3's forwarder Outer), decorated so that one loan set per local rejects it by accident; per-field loan sets accept it, and it reads the dead local n when run (r=7). The whole-body fallback catches it: a body with a call that may take part in that self-reach is checked whole (call.go remnantReach; mutant M-FIX12-remnant-whole). F's own check also rejects its write as a store of x into x (E0604, marked), so M-FIX12-remnant-whole alone does not flip it; FIX12-unsafe-u1 is that row's witness. This variant: FR34-E1-copy in r3b's form (a Cell reached through &S, forwarded by Outer).
struct S {
    a: &Cell[?&i32]
    b: &i32
}

struct Hd {
    s: S
}

struct TT {
    t: &i32
    s: &i32
}

F(h: mut Hd, x: S into h) {
    v := x.b
    h.s = x
    h.s.a.Store(some(v)) // <- E0604
}

Outer(px: &S) {
    dummy: i32 = 0
    dc := Cell[?&i32].New(none)
    var h := Hd(s: S(a: &dc, b: &dummy))
    F(mut h, *px)
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
    x0: i32 = 0
    cl := Cell[?&i32].New(none)
    var pp := TT(t: &x0, s: &x0)
    if sys.args.Len() >= 0 {
        n: i32 = 7
        pp.s = &n // <- E0505
        s := S(a: &cl, b: &n)
        Outer(&s)
        if r := cl.Load() {
            pp.t = r
        }
    }
    k := Clobber(5)
    w := pp.t
    v := *w
    try Print(mut out, "r={v} k={k}\n", (v, k))
}

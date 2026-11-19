// verdict: reject E0505 E0604
// note: self-reach through a forwarding parameter (FH-selfreach-r3's forwarder Outer), decorated so that one loan set per local rejects it by accident; per-field loan sets accept it, and it reads the dead local n when run (r=7). The whole-body fallback catches it: a body with a call that may take part in that self-reach is checked whole (call.go remnantReach; mutant M-FIX12-remnant-whole). F's own check also rejects its write as a store of x into x (E0604, marked), so M-FIX12-remnant-whole alone does not flip it; FIX12-unsafe-u1 is that row's witness. This variant: FR34-E4c-clause.mk through r3's forwarder Outer.
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

MkT(t: &i32, s: &i32): TT from (t: t, s: s) {
    return TT(t: t, s: s)
}

Outer(x: S) {
    dummy: i32 = 0
    var dr: &i32 = &dummy
    var h := Hd(s: S(a: mut dr, b: &dummy))
    F(mut h, x)
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
    var w: &i32 = &x0
    if sys.args.Len() >= 0 {
        n: i32 = 7
        Outer(S(a: mut r, b: &n))
        TT(t, ..) := MkT(r, &n)
        w = t // <- E0505
    }
    k := Clobber(5)
    v := *w
    try Print(mut out, "keep={keep} r={v} k={k}\n", (keep, v, k))
}

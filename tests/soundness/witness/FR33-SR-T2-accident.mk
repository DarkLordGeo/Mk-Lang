// verdict: reject E0501 E0504 E0505 E0604
// note: of the accident class: FR33-SR-A3-accident with a whole `mut pp` to a clause-free callee (KeepR). An exclusive borrow of the whole of a split local collapses it, so the strong field update does not take n's loan out of pp, and the program is E0505. F's own check rejects its write as a store of x into x (E0604, marked). A change of this verdict reopens the holes
struct S {
    a: mut &i32
    b: &i32
}

struct Hd {
    s: S
}

struct PR {
    a: mut &i32
    s: &i32
}

F(l: mut Hd, x: S into l) {
    v := x.b
    l.s = x
    *l.s.a = v // <- E0604
}

KeepR(p: mut PR) {
    p.s = p.s
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
    var q: &i32 = &x0
    var l := Hd(s: S(a: mut q, b: &x0))
    var pp := PR(a: mut r, s: &x0)
    if sys.args.Len() >= 0 {
        n: i32 = 7
        pp.s = &n // <- E0505
        F(mut l, S(a: mut *pp.a, b: &n)) // <- E0505
        KeepR(mut pp) // <- E0501
        pp.s = &x0
    }
    k := Clobber(5)
    v := *pp.a // <- E0504
    try Print(mut out, "keep={keep} r={v} k={k}\n", (keep, v, k))
}

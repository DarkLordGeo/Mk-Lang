// verdict: reject E0504 E0505 E0604
// note: one of the accident programs: one loan set per local rejects it only by accident, and per-field loan sets without the allocator store rule and the self-reach check accept it (`mk run` prints keep=0 r=7, n read after its scope). FR33-SR-A3-accident with a whole `&pp` to a clause-free callee (PeekR) before the strong update, so the shared whole borrow keeps the split. F's own check rejects its write as a store of x into x (E0604, marked). A change of this verdict reopens the holes
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

PeekR(p: &PR) {
    if *p.s > 100 {
        return
    }
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
        PeekR(&pp) // <- E0504
        pp.s = &x0
    }
    k := Clobber(5)
    v := *pp.a // <- E0504
    try Print(mut out, "keep={keep} r={v} k={k}\n", (keep, v, k))
}

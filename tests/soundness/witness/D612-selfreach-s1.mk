// verdict: reject E0505 E0604
// note: self-reach: a value stored into a holder brings a loan whose own `mut &` referent the callee then writes through the holder; the stored loans reach their own `mut` referents, so the program is rejected (the holder is an arena List). Accepted, it reads a dead local when run. Acceptance means the hole reopened; no single mutant row reopens it, since the allocator store rule, the self-reach check (a stored loan never reaches its own `mut` referent) and F's own check each reject it. Self-reach through a forwarding parameter is witnessed by FH-selfreach-r3
struct S {
    a: mut &i32
    b: &i32
}

F(l: mut List[S], x: S into l): OutOfMemory!void {
    v := x.b
    try l.Push(x)
    *l[0].a = v // <- E0604
}

Inner(p: mut &i32, ar: &Arena) {
    n: i32 = 7
    var l := List[S].New(ar)
    F(mut l, S(a: p, b: &n)) else { return } // <- E0505 E0604
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
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    keep: i32 = 0
    var r: &i32 = &keep
    Inner(mut r, &ar)
    k := Clobber(5)
    v := *r
    try Print(mut out, "keep={keep} r={v} k={k}\n", (keep, v, k))
}

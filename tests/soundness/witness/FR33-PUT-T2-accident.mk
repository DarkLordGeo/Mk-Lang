// verdict: reject E0505
// note: of the accident class: FR33-PUT-A6-accident with a whole `mut pp` to a clause-free callee (KeepP) between the hidden store and the strong update. An exclusive borrow of the whole of a split local collapses it, so the strong update `pp.s = &x0` does not take z's loan out of pp, and the program is E0505 at both marked lines. A change of this verdict reopens the holes
struct P {
    c: mut Cell[?&i32]
    s: &i32
}

StashP(p: &P, v: &i32 into p) {
    p.c.Store(some(v))
}

KeepP(p: mut P) {
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
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    x0: i32 = 0
    c := ar.Put(Cell[?&i32].New(none)) else { return }
    var pp := P(c: c, s: &x0)
    if sys.args.Len() >= 0 {
        z: i32 = 7
        pp.s = &z // <- E0505
        StashP(&pp, &z) // <- E0505
        KeepP(mut pp)
        pp.s = &x0
    }
    k := Clobber(5)
    if r := pp.c.Load() {
        v := *r
        try Print(mut out, "r={v} k={k}\n", (v, k))
    }
}

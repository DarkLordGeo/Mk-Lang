// verdict: reject E0505
// note: of the accident class: one loan set per local rejects it only by accident, and per-field loan sets without the allocator store rule and the self-reach check accept it (`mk run` prints r=7, z read after its scope). FR33-PUT-A3-accident with a whole rebuild `pp = P2(c: pp.c, s: &x0)` in place of the strong field update: the field read gives c's set, which lacks z. The call passes a holder whose loans may take part in a self-reach through a forwarding parameter, so the body is also checked whole, which rejects the field write too. A change of this verdict reopens the holes
struct H2 {
    c: &Cell[?&i32]
}

struct P2 {
    c: &Cell[?&i32]
    s: &i32
}

Stash2(h: &H2, v: &i32 into h) {
    h.c.Store(some(v))
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
    cs := &*c
    h := H2(c: cs)
    var pp := P2(c: cs, s: &x0)
    if sys.args.Len() >= 0 {
        z: i32 = 7
        pp.s = &z // <- E0505
        Stash2(&h, &z) // <- E0505
        pp = P2(c: pp.c, s: &x0)
    }
    k := Clobber(5)
    if r := pp.c.Load() {
        v := *r
        try Print(mut out, "r={v} k={k}\n", (v, k))
    }
}

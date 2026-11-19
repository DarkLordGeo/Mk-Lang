// verdict: reject E0505
// note: a later-hop allocator store: a loan stored through a holder into an allocator-held cell (Cell, List or Mutex, reached through a field, an option, an array, a list or a sub-arena) is stored into the allocator itself, so the program is rejected. Accepted, it reads a dead local or reset arena memory when run. Acceptance means the hole reopened (mutant M-C3-later)
struct H {
    m: &Mutex[?&i32]
}

StashM(io: Io, h: &H, v: &i32 into h) {
    var g := h.m.Lock(io) else { return }
    g.Store(some(v))
    g.Unlock()
}

Fill(io: Io, ar: &Arena): OutOfMemory!mut Mutex[?&i32] from ar {
    m := try ar.Put(Mutex[?&i32].New(none))
    h := H(m: &*m)
    z: i32 = 7
    scope s := io.Scope() {
        s.go StashM(io, &h, &z) // <- E0505
    }
    return m
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
    m := Fill(sys.io, &ar) else { return }
    k := Clobber(5)
    var g := m.Lock(sys.io) else { return }
    defer g.Unlock()
    if r := *g.Ref() {
        v := *r
        try Print(mut out, "r={v} k={k}\n", (v, k))
    }
}

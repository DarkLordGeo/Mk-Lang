// verdict: reject E0505
// note: a later-hop allocator store through an arena reached on a later hop: FH-d462-r1d with a local cell top in front of the cell hc in arena ar, so Stash3's store through top reaches ar on the second hop. If the walk stopped at a later-hop allocator, the loan b's cell c holds in ar's memory would not be followed, z would go into top and ar only, and c in b would keep &z after Fill returns (`mk run` then prints r=9005). The walk follows, out of a later-hop allocator, only what was stored into its contents (allocStored), so z reaches b and the program is rejected. Acceptance means the hole reopened (mutant M-C3-deep)
Stash2(h: &Cell[?&Cell[?&i32]], v: &i32 into h) {
    if inner := h.Load() {
        inner.Store(some(v))
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

Smash(n: i32): i32 {
    var a: [64]i32 = [0; 64]
    for i in 0..64 {
        a[i] = n + 9000
    }
    var s: i32 = 0
    for i in 0..64 {
        s = s + a[i]
    }
    return s
}

Stash3(t: &Cell[?&Cell[?&Cell[?&i32]]], v: &i32 into t) {
    if hc := t.Load() {
        if inner := hc.Load() {
            inner.Store(some(v))
        }
    }
}

Fill(b: &Arena, heap: &Heap): OutOfMemory!mut Cell[?&i32] from b {
    c := try b.Put(Cell[?&i32].New(none))
    z: i32 = 7
    var ar := Arena.New(heap) else { return c }
    hc := ar.Put(Cell[?&Cell[?&i32]].New(none)) else {
        ar.Free()
        return c
    }
    hc.Store(some(&*c))
    top := Cell[?&Cell[?&Cell[?&i32]]].New(some(&*hc))
    Stash3(&top, &z) // <- E0505
    ar.Free()
    return c
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var b := Arena.New(sys.heap) else { return }
    defer b.Free()
    c := Fill(&b, sys.heap) else { return }
    k := Smash(5)
    if r := c.Load() {
        v := *r
        try Print(mut out, "r={v} k={k}\n", (v, k))
    }
}

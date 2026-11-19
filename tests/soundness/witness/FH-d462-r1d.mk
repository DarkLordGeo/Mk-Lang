// verdict: reject E0505
// note: a later-hop allocator store through the allocator-from-allocator skip where the first arena is no sub-arena: ar is Arena.New(heap), a cell in it is filled with a reference to c, a cell in b, and Stash2 stores &z through it into c. A rule that skipped b because it is reached out of ar would leave &z in c in b after Fill returns (`mk run` then prints r=9005). b's loan is in ar's contents, not its provenance, so the walk reaches b and the program is rejected. Acceptance means the hole reopened (mutant M-C3-content)
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

Fill(b: &Arena, heap: &Heap): OutOfMemory!mut Cell[?&i32] from b {
    c := try b.Put(Cell[?&i32].New(none))
    z: i32 = 7
    var ar := Arena.New(heap) else { return c }
    hc := ar.Put(Cell[?&Cell[?&i32]].New(none)) else {
        ar.Free()
        return c
    }
    hc.Store(some(&*c))
    Stash2(&*hc, &z) // <- E0505
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

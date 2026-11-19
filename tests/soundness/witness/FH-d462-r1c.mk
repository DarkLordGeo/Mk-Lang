// verdict: reject E0503 E0505
// note: a later-hop allocator store through the sub-arena exception: Fill's sub-arena ar of the parameter arena b gets a cell hc filled with a reference to c, a cell in b, so ar's contents hold b's loan; Stash2 then stores &z through hc into c. The walk reaches ar first and b out of ar. A rule that skipped every allocator reached out of an allocator would put z into ar only, which ends first, while c in b kept &z (`mk run` then prints r=9005). The skip applies only to a loan the holder has by provenance (the loan its own value was made with: a sub-arena's parent, an arena's heap), not one stored into its contents, so z reaches b and the program is rejected. Acceptance means the hole reopened (mutant M-C3-content). The loan of c that the arena's list holds is kept live through the parameter's node (absPredecessors), so the return of c is also E0503 (marked).
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

Fill(b: &Arena): OutOfMemory!mut Cell[?&i32] from b {
    c := try b.Put(Cell[?&i32].New(none))
    z: i32 = 7
    var ar := b.Sub() else { return c }
    hc := ar.Put(Cell[?&Cell[?&i32]].New(none)) else {
        ar.Free()
        return c
    }
    hc.Store(some(&*c))
    Stash2(&*hc, &z) // <- E0505
    ar.Free()
    return c // <- E0503
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var b := Arena.New(sys.heap) else { return }
    defer b.Free()
    c := Fill(&b) else { return }
    k := Smash(5)
    if r := c.Load() {
        v := *r
        try Print(mut out, "r={v} k={k}\n", (v, k))
    }
}

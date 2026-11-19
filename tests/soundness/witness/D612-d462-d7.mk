// verdict: reject E0503 E0505
// note: a later-hop allocator store: a loan stored through a holder into an allocator-held cell (Cell, List or Mutex, reached through a field, an option, an array, a list or a sub-arena) is stored into the allocator itself, so the program is rejected. Accepted, it reads a dead local or reset arena memory when run. Acceptance means the hole reopened (mutant M-C3-later). The loan of c that the arena's list holds is kept live through the parameter's node (absPredecessors), so the return of c is also E0503 (marked).
Stash(l: &List[&Cell[?&i32]], v: &i32 into l) {
    l[0].Store(some(v))
}

Fill(ar: &Arena): OutOfMemory!mut Cell[?&i32] from ar {
    c := try ar.Put(Cell[?&i32].New(none))
    var l := List[&Cell[?&i32]].New(ar)
    try l.Push(&*c)
    z: i32 = 7
    Stash(&l, &z) // <- E0505
    return c // <- E0503
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
    c := Fill(&ar) else { return }
    k := Clobber(5)
    if r := c.Load() {
        v := *r
        try Print(mut out, "r={v} k={k}\n", (v, k))
    }
}

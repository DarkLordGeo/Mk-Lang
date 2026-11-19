// verdict: reject E0501 E0503
// note: a later-hop allocator store: a loan stored through a holder into an allocator-held cell (Cell, List or Mutex, reached through a field, an option, an array, a list or a sub-arena) is stored into the allocator itself, so the program is rejected. Accepted, it reads a dead local or reset arena memory when run. Acceptance means the hole reopened (mutant M-C3-later)
struct H {
    c: &Cell[?&i32]
}

Stash(h: &H, v: &i32 into h) {
    h.c.Store(some(v))
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    var tmp := Arena.New(sys.heap) else { return }
    defer tmp.Free() // <- E0503
    c := ar.Put(Cell[?&i32].New(none)) else { return }
    h := H(c: &*c)
    x := tmp.Put(i32(7)) else { return }
    Stash(&h, &*x)
    tmp.Reset() // <- E0501
    y := tmp.Put(i32(99)) else { return }
    if r := c.Load() {
        v := *r
        w := *y
        try Print(mut out, "stored 7, read {v} (new contents {w})\n", (v, w))
    }
}

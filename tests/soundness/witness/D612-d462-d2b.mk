// verdict: reject E0505
// note: a later-hop allocator store: a loan stored through a holder into an allocator-held cell (Cell, List or Mutex, reached through a field, an option, an array, a list or a sub-arena) is stored into the allocator itself, so the program is rejected. Accepted, it reads a dead local or reset arena memory when run. Acceptance means the hole reopened (mutant M-C3-later)
struct H {
    c: mut Cell[?&i32]
}

Stash(h: &H, v: &i32 into h) {
    h.c.Store(some(v))
}

struct Ctx {
    a: &Arena
    name: str
}

Fill(c: Ctx): OutOfMemory!mut Cell[?&i32] from c {
    a := c.a
    cl := try a.Put(Cell[?&i32].New(none))
    h := H(c: cl)
    z: i32 = 7
    Stash(&h, &z) // <- E0505
    H(c: back) := h
    return back
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
    c := Fill(Ctx(a: &ar, name: "n")) else { return }
    k := Clobber(5)
    if r := c.Load() {
        v := *r
        try Print(mut out, "r={v} k={k}\n", (v, k))
    }
}

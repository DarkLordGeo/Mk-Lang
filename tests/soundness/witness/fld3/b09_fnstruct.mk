// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. S { f: fn, n: i32 } has a fn inline, so &S is not shallow (an opaque type inline in the referent). The rejection itself is G's y: str into c, a direct store into the cell through c: &Cell[str], which shallow loans do not touch
// before FLD-3: reject E0503 E0505 at 35 E0503, 37 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct S {
    f: fn(c: &Cell[str], y: str into c)
    n: i32
}

Put(c: &Cell[str], y: str into c) {
    c.Store(y)
}

F(l: mut List[&S], s: &S into l): OutOfMemory!void {
    try l.Push(s)
}

G(l: mut List[&S], c: &Cell[str], y: str into c) {
    l[0].f(c, y)
}

Yes(): bool {
    return true
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    cell := Cell[str].New("init")
    s := S(f: Put, n: 1)
    var l := List[&S].New(&ar)
    F(mut l, &s) else { return }
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        G(mut l, &cell, y) // <- E0505
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

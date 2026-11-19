// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. c: &Cell[str] into a, a: str into b, b: &i32 into l: the cell loan enters a deep (its argument is &Cell[str], whose referent is a carrier and writable by type) and keeps that mark when the later clause chains it into l (a chained loan keeps the mark it first had, never re-judged by the middle's type), so G's y into l walks l through it into cell and the arena string outlives ar2. Two shallow middles; the cell loan stays deep through both
// before FLD-3: reject E0503 E0505 at 27 E0503, 29 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
F(l: mut List[&Cell[str]], c: &Cell[str] into a, a: str into b, b: &i32 into l): OutOfMemory!void {
    try l.Push(c)
}

G(l: mut List[&Cell[str]], y: str into l) {
    l[0].Store(y)
}

Yes(): bool {
    return true
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    cell := Cell[str].New("init")
    var l := List[&Cell[str]].New(&ar)
    s0 := "abc"
    n: i32 = 1
    F(mut l, &cell, s0, &n) else { return }
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        G(mut l, y) // <- E0505
    }
    var ar3 := Arena.New(sys.heap) else { return }
    defer ar3.Free()
    s2 := ar3.Dup("XXXXXXXXXX") else { return }
    v := cell.Load()
    try Print(mut out, "cell={v} s2={s2}\n", (v, s2))
}

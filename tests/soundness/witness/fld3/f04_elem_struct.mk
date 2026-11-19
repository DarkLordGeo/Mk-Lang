// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. N2 { s: str, c: &Cell[str] } by value is not shallow (a carrier and writable referent), so cs.Push(n2) lands the cell loan deep, and cs[0].c.Store(y) reaches cell (and ar at the walk's first hop, hence 'borrowed by ar'). The control for a struct element holding a Cell reference
// before FLD-3: reject E0503 E0505 at 29 E0503, 31 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct N2 {
    s: str
    c: &Cell[str]
}

AddS(l: mut List[str], s: str into l): OutOfMemory!void {
    try l.Push(s)
}

Yes(): bool {
    return true
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    cell := Cell[str].New("init")
    n2 := N2(s: "nm", c: &cell)
    var l := List[str].New(&ar)
    AddS(mut l, n2.s) else { return }
    var cs := List[N2].New(&ar)
    cs.Push(n2) else { return }
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        cs[0].c.Store(y) // <- E0505
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

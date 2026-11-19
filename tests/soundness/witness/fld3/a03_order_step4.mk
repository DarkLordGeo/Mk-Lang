// verdict: reject E0503 E0505 E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. *q into l with q: &List[&Cell[str]]: that store judges q's referent, List[&Cell[str]], a view, so the cell loans land deep and the round's x into l reaches cell. Judged by q's own type it is deep as well
// before FLD-3: reject E0503 E0505 E0604 at 6 E0604, 22 E0503, 25 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
F(l: mut List[&Cell[str]], x: str into l, q: &List[&Cell[str]]) *q into l {
    l.Push(q[0]) else { return }
    l[0].Store(x) // <- E0604
}

Yes(): bool {
    return true
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    cell := Cell[str].New("init")
    var cells := List[&Cell[str]].New(&ar)
    cells.Push(&cell) else { return }
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        s := ar2.Dup("secret-old") else { return }
        var l := List[&Cell[str]].New(&ar)
        F(mut l, s, &cells) // <- E0505
    }
    var ar3 := Arena.New(sys.heap) else { return }
    defer ar3.Free()
    s2 := ar3.Dup("XXXXXXXXXX") else { return }
    v := cell.Load()
    try Print(mut out, "cell={v} s2={s2}\n", (v, s2))
}

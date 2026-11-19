// verdict: reject E0503 E0505 E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. x: str into l runs first and its loan lands shallow; c: &Cell[str] into l then lands the cell loan deep; the clause rounds rerun x into l, whose walk goes through the deep cell loan into cell. No mark the walk passes is shallow. A twin of the rounds: shallow loans must not end them early
// before FLD-3: reject E0503 E0505 E0604 at 6 E0604, 20 E0503, 23 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
F(l: mut List[&Cell[str]], x: str into l, c: &Cell[str] into l): OutOfMemory!void {
    try l.Push(c)
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
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        s := ar2.Dup("secret-old") else { return }
        var l := List[&Cell[str]].New(&ar)
        F(mut l, s, &cell) else { return } // <- E0505
    }
    var ar3 := Arena.New(sys.heap) else { return }
    defer ar3.Free()
    s2 := ar3.Dup("XXXXXXXXXX") else { return }
    v := cell.Load()
    try Print(mut out, "cell={v} s2={s2}\n", (v, s2))
}

// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. F[T: Plain, U: Plain] with U = &Cell[str] and T = str: the cell loan enters a deep and keeps its mark through a: T into l, so G reaches cell. A mark reads the argument's own type as passed, here str, never the generic parameter's declared T (read that way, List[T].Push could never mark a loan shallow, and the copies in p-clause.mk would owe `*xs into xs`)
// before FLD-3: reject E0503 E0505 at 26 E0503, 28 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
F[T: Plain, U: Plain](l: mut List[U], c: U into a, a: T into l): OutOfMemory!void {
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
    F(mut l, &cell, s0) else { return }
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

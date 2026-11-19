// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. AddName(Name(c)) before the loop (shallow), Poke then AddCell(c) inside it (deep on the back edge): the loop head's join keeps the loan deep, so the first Poke of the fixpoint reaches cell; the second iteration stores the new arena's string into cell
// before FLD-3: reject E0503 E0505 at 45 E0503, 47 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct K {
    names: List[str]
    cells: List[&Cell[str]]
}

AddName(k: mut K, s: str into k): OutOfMemory!void {
    try k.names.Push(s)
}

AddCell(k: mut K, c: &Cell[str] into k): OutOfMemory!void {
    try k.cells.Push(c)
}

Poke(k: mut K, y: str into k) {
    for i in 0..k.cells.Len() {
        k.cells[i].Store(y)
    }
}

Name(c: &Cell[str]): str from c {
    return "nm"
}

Yes(): bool {
    return true
}

No(): bool {
    return false
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    cell := Cell[str].New("init")
    c := &cell
    var k := K(names: List[str].New(&ar), cells: List[&Cell[str]].New(&ar))
    AddName(mut k, Name(c)) else { return }
    for _ in 0..2 {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        Poke(mut k, y) // <- E0505
        AddCell(mut k, c) else { return }
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

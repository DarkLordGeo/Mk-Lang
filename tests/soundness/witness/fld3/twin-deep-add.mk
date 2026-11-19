// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. One loan, the borrow &cell held by c, reaches k shallow (AddName(mut k, Name(c)): Name's str result holds c's loan) and then deep (AddCell(mut k, c)): the deep add wins, so Poke reaches cell. No struct field is read. With the earlier shallow mark kept, Poke would skip the loan (w-deepadd.mk shows the acceptance that follows)
// before FLD-3: reject E0503 E0505 at 46 E0503, 48 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
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
    AddCell(mut k, c) else { return }
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        Poke(mut k, y) // <- E0505
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

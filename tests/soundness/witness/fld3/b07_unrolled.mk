// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. AddName(h.name) then AddCell(h.slot), then Poke. h is split, so these are two different loans, and Poke reaches cell through the deep one. twin-deep-add.mk tests one loan that arrives shallow, then deep
// before FLD-3: reject E0503 E0505 at 43 E0503, 45 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct H {
    name: str
    slot: &Cell[str]
}

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

Yes(): bool {
    return true
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    cell := Cell[str].New("init")
    h := H(name: "nm", slot: &cell)
    var k := K(names: List[str].New(&ar), cells: List[&Cell[str]].New(&ar))
    AddName(mut k, h.name) else { return }
    AddCell(mut k, h.slot) else { return }
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        Poke(mut k, y) // <- E0505
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

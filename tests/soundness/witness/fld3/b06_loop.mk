// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. AddName(h.name) before the loop, then Poke and AddCell(h.slot) in it: the cell loan reaches k deep through AddCell, the loop head's join keeps it deep (deep wins), and the second iteration's Poke stores into cell. h is split, so h.name holds only name's loans and one loan is never both shallow and deep in k here; twin-loop.mk tests the join
// before FLD-3: reject E0503 E0505 at 38 E0503, 40 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
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

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    cell := Cell[str].New("init")
    h := H(name: "nm", slot: &cell)
    var k := K(names: List[str].New(&ar), cells: List[&Cell[str]].New(&ar))
    AddName(mut k, h.name) else { return }
    for _ in 0..2 {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        Poke(mut k, y) // <- E0505
        AddCell(mut k, h.slot) else { return }
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

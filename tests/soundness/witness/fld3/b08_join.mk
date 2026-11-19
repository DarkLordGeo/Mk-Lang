// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. One branch AddName(h.name), the other AddCell(h.slot): the join keeps the cell loan deep, and the AddName after the join does not lower it. h is split, as in b06_loop.mk, so twin-join.mk tests the join itself. Yes() takes the AddName branch, so a run shows no use after free: the rejection is static
// before FLD-3: reject E0503 E0505 at 47 E0503, 49 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
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
    if Yes() {
        AddName(mut k, h.name) else { return }
    } else {
        AddCell(mut k, h.slot) else { return }
    }
    AddName(mut k, h.name) else { return }
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        Poke(mut k, y) // <- E0505
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

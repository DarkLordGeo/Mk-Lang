// verdict: reject E0503 E0505
// note: a witness with no arena, so a store into an allocator cannot catch it by accident: the loan reaches k shallow on one arm and deep on the other: deep wins at the join. Witness of M-F3-join-keeps-shallow
// before FLD-3: reject E0503 E0505 at 47 E0503, 49 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct K {
    c: ?&Cell[str]
    n: ?str
}

AddName(k: mut K, s: str into k) {
    k.n = some(s)
}

AddCell(k: mut K, c: &Cell[str] into k) {
    k.c = some(c)
}

Poke(k: mut K, y: str into k) {
    if c := k.c {
        c.Store(y)
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
    cell := Cell[str].New("init")
    c := &cell
    var k := K(c: none, n: none)
    if No() {
        AddName(mut k, Name(c))
    } else {
        AddCell(mut k, c)
    }
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        Poke(mut k, y) // <- E0505
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

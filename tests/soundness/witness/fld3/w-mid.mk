// verdict: reject E0503 E0505
// note: a witness with no arena, so a store into an allocator cannot catch it by accident: c: &Cell[str] into a, a: str into k: the cell loan enters a deep and keeps that mark when the chain carries it into k (an `x into p` store's marks come from each argument's type as passed, never re-judged by the middle's). Witness of M-F3-mark-from-middle
// before FLD-3: reject E0503 E0505 at 34 E0503, 36 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct K {
    c: ?&Cell[str]
}

F(k: mut K, c: &Cell[str] into a, a: str into k) {
    k.c = some(c)
}

G(k: mut K, y: str into k) {
    if c := k.c {
        c.Store(y)
    }
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
    var k := K(c: none)
    s0 := "abc"
    F(mut k, &cell, s0)
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        G(mut k, y) // <- E0505
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

// verdict: reject E0503 E0505
// note: a witness with no arena, so a store into an allocator cannot catch it by accident: c: &Cell[str] into q, *q into k with q: &Nm (a shallow referent): the loan chained into q this call keeps its deep mark through the `*q into k` store, whatever q's referent type. Witness of M-F3-star-chain
// before FLD-3: reject E0503 E0505 at 35 E0503, 37 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct Nm {
    s: str
    k: i32
}

struct K {
    c: ?&Cell[str]
}

F(k: mut K, q: &Nm, c: &Cell[str] into q) *q into k {
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

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    cell := Cell[str].New("init")
    var k := K(c: none)
    nm := Nm(s: "abc", k: 1)
    F(mut k, &nm, &cell)
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        G(mut k, y) // <- E0505
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

// verdict: reject E0503 E0505
// note: a witness with no arena, so a store into an allocator cannot catch it by accident: &H with H { name: str, slot: &Cell[str] }: H is a carrier but not writable by type, so only the carrier test keeps the loan deep. Witness of M-F3-carrier-referent
// before FLD-3: reject E0503 E0505 at 39 E0503, 41 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct K {
    c: ?&Cell[str]
}

struct H {
    name: str
    slot: &Cell[str]
}

F(k: mut K, h: &H into k) {
    k.c = some(h.slot)
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
    h := H(name: "h", slot: &cell)
    F(mut k, &h)
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        G(mut k, y) // <- E0505
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

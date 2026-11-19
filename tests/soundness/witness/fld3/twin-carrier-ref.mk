// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. h: &H into l with H { name: str, slot: &Cell[str] }: H is a carrier but not writable by type (its Cell is behind a reference), so only the carrier test keeps &H deep; Poke's walk goes l, h (shared), then the Cell, which restores writing. Without the carrier test, &H would be judged shallow (w-carrier.mk shows the acceptance that follows)
// before FLD-3: reject E0503 E0505 at 33 E0503, 35 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct H {
    name: str
    slot: &Cell[str]
}

AddH(l: mut List[&H], h: &H into l): OutOfMemory!void {
    try l.Push(h)
}

Poke(l: mut List[&H], y: str into l) {
    for i in 0..l.Len() {
        l[i].slot.Store(y)
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
    var l := List[&H].New(&ar)
    AddH(mut l, &h) else { return }
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        Poke(mut l, y) // <- E0505
    }
    v := cell.Load()
    try Print(mut out, "cell={v}\n", (v,))
}

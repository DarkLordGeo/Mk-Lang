// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. l.Push(y) lands the arena loan shallow in l; v = r[0] reads it through r := &l, so v gets every loan l holds, the shallow one included, and v outlives ar2
// before FLD-3: reject E0503 E0505 at 16 E0503, 18 E0505, 20 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Yes(): bool {
    return true
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    var l := List[str].New(&ar)
    var v := "keep"
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        y := ar2.Dup("secret-old") else { return }
        l.Push(y) else { return } // <- E0505
        r := &l
        v = r[0] // <- E0505
    }
    try Print(mut out, "v={v}\n", (v,))
}

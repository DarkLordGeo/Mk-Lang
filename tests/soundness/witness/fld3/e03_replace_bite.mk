// verdict: reject E0503 E0505
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. Replace's x: str into dst: the arena loan lands shallow in a, which outlives ar2: E0503 and E0505 (liveness sees every loan)
// before FLD-3: reject E0503 E0505 at 13 E0503, 15 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Yes(): bool {
    return true
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var a := "keep"
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        b := ar2.Dup("secret-old") else { return }
        _ = Replace(mut a, b) // <- E0505
    }
    try Print(mut out, "a={a}\n", (a,))
}

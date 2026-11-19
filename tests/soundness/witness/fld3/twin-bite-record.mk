// verdict: reject E0505 E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. The bite that c07 does not have: CopyR is c07's, and owes no `*xs into xs` (Nm { s: str, k: i32, r: &i32 } is shallow); its E0604 asks for `*xs into a`, since out holds a loan of a. z dies while cp still holds its loan through src: E0505
// before FLD-3: reject E0505 E0604 at 13 E0604, 30 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct Nm {
    s: str
    k: i32
    r: &i32
}

CopyR(a: &Arena, xs: &List[Nm]): OutOfMemory!List[Nm] from a, xs {
    var out := List[Nm].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i]) // <- E0604
    }
    return out
}

Yes(): bool {
    return true
}

export Main(sys: &System): WriteError!void {
    var o := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    var src := List[Nm].New(&ar)
    var cp := List[Nm].New(&ar)
    if Yes() {
        z: i32 = 7
        src.Push(Nm(s: "a", k: 1, r: &z)) else { return } // <- E0505
        cp = CopyR(&ar, &src) else { return }
    }
    n := *cp[0].r
    try Print(mut o, "n={n}\n", (n,))
}

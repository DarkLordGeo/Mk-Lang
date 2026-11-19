// verdict: reject E0503 E0505 E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. The bite that c06 does not have: Copy2 is c06's, and owes no `*xs into xs` (?str is shallow); its E0604 asks for `*xs into a`, since out holds a loan of a. Main frees ar2 while cp still holds its loan through src: E0503 and E0505 (liveness sees every loan; Main reads only Copy2's signature)
// before FLD-3: reject E0503 E0505 E0604 at 7 E0604, 24 E0503, 26 E0505 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Copy2(a: &Arena, xs: &List[?str]): OutOfMemory!List[?str] from a, xs {
    var out := List[?str].New(a)
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
    var src := List[?str].New(&ar)
    var cp := List[?str].New(&ar)
    if Yes() {
        var ar2 := Arena.New(sys.heap) else { return }
        defer ar2.Free() // <- E0503
        s := ar2.Dup("secret-old") else { return }
        src.Push(some(s)) else { return } // <- E0505
        cp = Copy2(&ar, &src) else { return }
    }
    if v := cp[0] {
        try Print(mut o, "v={v}\n", (v,))
    }
}

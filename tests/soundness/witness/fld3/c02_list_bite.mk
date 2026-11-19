// verdict: reject E0402 E0503 E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. for s in xs.Items() gives s: str; out.Push(s) lands xs's loan shallow in out, so the second push owes no `*xs into xs`. The E0604 that remains asks for `*xs into a`: out holds a loan of a, so the store reaches a. Main frees ar2 while cp holds its loan through src: E0503 (liveness sees every loan); E0402 is Main's early return
// before FLD-3: reject E0402 E0503 E0604 at 7 E0604, 16 E0402, 21 E0503 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Copy(a: &Arena, xs: &List[str]): OutOfMemory!List[str] from a, xs {
    var out := List[str].New(a)
    for s in xs.Items() {
        try out.Push(s) // <- E0604
    }
    return out
}

export Main(sys: &System): WriteError!void {
    var o := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    var ar2 := Arena.New(sys.heap) else { return } // <- E0402
    s := ar2.Dup("secret-old") else { return }
    var src := List[str].New(&ar)
    src.Push(s) else { return }
    cp := Copy(&ar, &src) else { return }
    ar2.Free() // <- E0503
    v := cp[0]
    try Print(mut o, "v={v}\n", (v,))
}

// verdict: reject E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. ?str is shallow (every reference inline in it is shared, to u8), so both pushes land xs's loan shallow in out and neither later push walks through it: no `*xs into xs` is owed. The E0604 that remains asks for `*xs into a`: out holds a loan of a, so the store reaches a (p-clause.mk writes that clause)
// before FLD-3: reject E0604 at 6 E0604, 7 E0604, 13 E0604 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Copy(a: &Arena, xs: &List[?str], c: &Cell[?str], y: str) : OutOfMemory!void {
    var out := List[?str].New(a)
    try out.Push(xs[0]) // <- E0604
    try out.Push(xs[1]) // <- E0604
}

Copy2(a: &Arena, xs: &List[?str]): OutOfMemory!List[?str] from a, xs {
    var out := List[?str].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i]) // <- E0604
    }
    return out
}

export Main(sys: &System) {
}

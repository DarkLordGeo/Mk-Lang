// verdict: reject E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. xs[i] through &HeapList[str] is a shallow str, so the second push does not walk through the first one's loan and no `*xs into xs` is owed (nothing can be written through a str). The E0604 that remains asks for `*xs into a`: out holds a loan of a, so the store reaches a.
// before FLD-3: reject E0604 at 7 E0604 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Copy(a: &Arena, xs: &HeapList[str]): OutOfMemory!void {
    var out := List[str].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i]) // <- E0604
    }
}

export Main(sys: &System) {
}

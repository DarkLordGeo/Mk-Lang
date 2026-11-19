// verdict: reject E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. The first push's E0604 asks for `*xs into a` (out holds a loan of a). &Cell[str] is not shallow, so out[0].Store(y) walks from the element's loans to the parameters' nodes and stores y there (E0604 names a, the first root found). The control for the c files: a deep element is still walked
// before FLD-3: reject E0604 at 6 E0604, 7 E0604 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Copy(a: &Arena, xs: &List[&Cell[str]], y: str): OutOfMemory!void {
    var out := List[&Cell[str]].New(a)
    try out.Push(xs[0]) // <- E0604
    out[0].Store(y) // <- E0604
}

export Main(sys: &System) {
}

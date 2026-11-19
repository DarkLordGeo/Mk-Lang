// verdict: reject E0604
// note: the control of p-clause.mk, with a deep element, &Cell[str]: its loan lands deep and the next push walks through it into xs, so `*xs into xs` stays owed
// before FLD-3: reject E0604 at 7 E0604 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
CopyC(a: &Arena, xs: &List[&Cell[str]]): OutOfMemory!List[&Cell[str]] from a, xs *xs into a {
    var out := List[&Cell[str]].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i]) // <- E0604
    }
    return out
}

export Main(sys: &System) {
}

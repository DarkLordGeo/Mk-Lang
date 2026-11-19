// verdict: reject E0602 E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. xs[i] is a shallow str, so no `*xs into xs` is owed. The E0604 that remains asks for `*xs into a`: out holds a loan of a, so the store reaches a (p-clause.mk writes that clause). The E0602 stays: the result holds xs's loan, which the signature does not name (results see every loan)
// before FLD-3: reject E0602 E0604 at 7 E0604, 9 E0602 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Copy(a: &Arena, xs: &List[str]): OutOfMemory!List[str] from a {
    var out := List[str].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i]) // <- E0604
    }
    return out // <- E0602
}

export Main(sys: &System) {
}

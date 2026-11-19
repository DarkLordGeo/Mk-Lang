// verdict: reject E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. [2]str and Nm { s: str, k: i32, r: &i32 } are shallow (an array, and a non-view struct of shallow fields), so no `*xs into xs` is owed. The E0604 that remains asks for `*xs into a`: out holds a loan of a, so the store reaches a (p-clause.mk writes that clause)
// before FLD-3: reject E0604 at 13 E0604, 21 E0604 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
struct Nm {
    s: str
    k: i32
    r: &i32
}

CopyA(a: &Arena, xs: &List[[2]str]): OutOfMemory!List[[2]str] from a, xs {
    var out := List[[2]str].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i]) // <- E0604
    }
    return out
}

CopyR(a: &Arena, xs: &List[Nm]): OutOfMemory!List[Nm] from a, xs {
    var out := List[Nm].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i]) // <- E0604
    }
    return out
}

export Main(sys: &System) {
}

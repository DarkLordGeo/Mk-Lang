// verdict: reject E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. Line 7 owes no `*xs into xs` (a shallow str into the local out); its E0604 asks for `*xs into a`, since out holds a loan of a. Line 9's E0604: dst.Push(out) passes a List[str] by value, a view, so out's loans (a's and xs's) land deep in dst's referent, which the signature does not declare
// before FLD-3: reject E0604 at 7 E0604, 9 E0604 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Copy(a: &Arena, dst: mut List[List[str]], xs: &List[str]): OutOfMemory!void {
    var out := List[str].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i]) // <- E0604
    }
    try dst.Push(out) // <- E0604
}

export Main(sys: &System) {
}

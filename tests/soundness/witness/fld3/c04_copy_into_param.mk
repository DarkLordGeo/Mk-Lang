// verdict: reject E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. Push stores xs[i]'s loans into out's own referent, a parameter's node reached at the walk's first hop through out's own exclusive loan, not through a shallow loan: *xs into out is a real clause (the caller's list holds xs's contents)
// before FLD-3: reject E0604 at 6 E0604 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Copy(out: mut List[str], xs: &List[str]): OutOfMemory!void {
    for i in 0..xs.Len() {
        try out.Push(xs[i]) // <- E0604
    }
}

export Main(sys: &System) {
}

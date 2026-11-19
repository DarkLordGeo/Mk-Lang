// verdict: reject E0508
// rule: D-422
// note: the array must be borrowable mut, as for xs[i].f = v
struct Counter {
    n: int
}

Counter.Bump(mut self) {
    self.n += 1
}

Local() {
    cs := [Counter(n: 0)]
    cs[0].Bump() // <- E0508
}

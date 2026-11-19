// verdict: reject E0504
// rule: D-422
// note: all indices of one array are one place, so another element cannot be read during the mut call
struct Counter {
    n: int
}

Counter.Take(mut self, other: &Counter) {
    self.n += other.n
}

Merge(cs: mut []Counter) {
    cs[0].Take(&cs[1]) // <- E0504
}

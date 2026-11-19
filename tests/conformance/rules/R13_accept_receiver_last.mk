// verdict: ok
// rule: R13
// note: the mut receiver is formed after the other arguments
struct Counter {
    n: int
}

Counter.Add(mut self, k: int) {
    self.n += k
}

Counter.Get(&self): int {
    return self.n
}

Double(c: mut Counter) {
    c.Add(c.Get())
}

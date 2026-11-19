// verdict: reject E0507
// rule: D-422
// note: a shared slice gives no mut place
struct Counter {
    n: int
}

Counter.Bump(mut self) {
    self.n += 1
}

BumpAt(cs: []Counter, i: int) {
    cs[i].Bump() // <- E0507
}

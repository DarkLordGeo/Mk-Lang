// verdict: reject E0301
// rule: D-420
// note: a destructured loop variable needs a record payload
struct Countdown {
    n: int
}

Countdown.Next(mut self): ?int {
    if self.n == 0 {
        return none
    }
    self.n -= 1
    return some(self.n)
}

Loop() {
    for (a, b) in Countdown(n: 1) { // <- E0301
        _ = a
        _ = b
    }
}

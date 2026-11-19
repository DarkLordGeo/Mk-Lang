// verdict: reject E0301
// rule: D-438
// note: an iterator source is taken by value only: & or mut before it is refused
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
    var it := Countdown(n: 3)
    for x in &it { // <- E0301
        _ = x
    }
    for x in mut it { // <- E0301
        _ = x
    }
}

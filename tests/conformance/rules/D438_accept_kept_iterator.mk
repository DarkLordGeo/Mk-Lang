// verdict: ok
// rule: D-438
// note: for x := it.Next() iterates an iterator that is kept; for x in it copies a plain iterator, so it is unchanged after
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

Kept(): int {
    var it := Countdown(n: 4)
    var s := 0
    for x := it.Next() {
        if x == 2 {
            break
        }
    }
    for x in it {
        s += x
    }
    return s + it.n
}

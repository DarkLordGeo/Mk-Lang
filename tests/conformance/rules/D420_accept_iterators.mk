// verdict: ok
// rule: D-420
// note: for over any value with Next, a destructured record payload, and a destructured element of a slice of plain records
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

struct Peek {
    n: int
}

Peek.Next(&self): ?(a: int, b: int) {
    if self.n == 0 {
        return none
    }
    return some((a: self.n, b: 1))
}

CountCode(text: str): int {
    var n := 0
    for line in text.Lines() {
        if line.StartsWith("#") {
            continue
        }
        n += 1
    }
    return n
}

Total(m: &Map[str, int]): int {
    var sum := 0
    for (key, value) in m.Iter() {
        _ = key
        sum += *value
    }
    return sum
}

Down(): int {
    var s := 0
    for k in Countdown(n: 3) {
        s += k
    }
    for (a: x, b: y) in Peek(n: 0) {
        s += x + y
    }
    return s
}

Records(xs: [](a: int, b: int)): int {
    var s := 0
    for (a, b) in xs {
        s += a * b
    }
    for i, (a: x, b: y) in xs {
        s += i + x + y
    }
    return s
}

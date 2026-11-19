// verdict: ok
// rule: D-422
// note: an index receiver on a user Slice[T] implementer goes through ItemsMut, and a type that is both a Slice[T] implementer and has Next iterates as a Slice[T], index form included
struct Counter {
    n: int
}

Counter.Bump(mut self) {
    self.n += 1
}

struct Bank {
    cs: [3]Counter
}

impl Slice[Counter] for Bank {
    Items(&self): []Counter {
        return self.cs[0..]
    }

    ItemsMut(mut self): mut []Counter {
        return mut self.cs[0..]
    }
}

Bank.Next(mut self): ?str {
    return none
}

Run(): int {
    var b := Bank(cs: [Counter(n: 0), Counter(n: 1), Counter(n: 2)])
    b[2].Bump()
    b[0].Bump()
    var s := 0
    for i, c in b {
        s += i * 10 + c.n
    }
    return s
}

// verdict: reject E0508
// rule: D-422
// note: through a user Slice[T] implementer, a local that is not var gives no mut place
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

NotVar(): int {
    b := Bank(cs: [Counter(n: 0), Counter(n: 1), Counter(n: 2)])
    b[1].Bump() // <- E0508
    return b[1].n
}

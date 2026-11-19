// verdict: reject E0301
// rule: D-420
// note: a Slice[T] implementer that also has Next iterates as a Slice[T], so the loop variable is the element, not the payload of Next
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

Elem(b: &Bank) {
    for c in b {
        t: str = c // <- E0301
        _ = t
    }
}

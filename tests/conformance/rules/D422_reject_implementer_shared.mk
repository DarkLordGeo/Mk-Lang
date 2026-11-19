// verdict: reject E0507
// rule: D-422
// note: through a user Slice[T] implementer, a shared reference gives no mut place for ItemsMut
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

Shared(b: &Bank) {
    b[0].Bump() // <- E0507
}

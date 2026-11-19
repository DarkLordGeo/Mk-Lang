// verdict: reject E0701
// rule: D-441
// note: SpinGuard.Store of a linear value would drop the old one (Replace(g.Mut(), x) gives it back), and Set.Remove drops its stored element
import std.atomic

linear struct Lease {
    id: u64
}

impl Eq for Lease {
    Eq(&self, other: &Lease): bool {
        return self.id == other.id
    }
}

impl Hash for Lease {
    Hash(&self, h: mut Hasher) {
        h.U64(self.id)
    }
}

Put(g: mut atomic.SpinGuard[Res], r: Res) {
    g.Store(r) // <- E0701
}

Drop(s: mut Set[Lease], x: &Lease): bool {
    return s.Remove(x) // <- E0701
}

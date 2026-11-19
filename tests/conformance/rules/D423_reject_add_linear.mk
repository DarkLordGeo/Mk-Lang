// verdict: reject E0701
// rule: D-423
// note: TextMap.Set with a linear value that is not Free, and an arena Set.Add of a linear element, would drop what they replace
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

Put(a: &Alloc, m: mut TextMap[Res], r: Res): OutOfMemory!void {
    try m.Set(a, "k", r) // <- E0701
}

Add(s: mut Set[Lease], x: Lease): OutOfMemory!bool {
    return try s.Add(x) // <- E0701
}

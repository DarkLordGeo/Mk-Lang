// verdict: reject E0207
// needs: types
// note: a member found only by folding is reported by the checker, in the types group
struct Bag {
    count: int
}

Bag.Len(&self): int {
    return self.count
}

Size(b: &Bag): int {
    return b.len() // <- E0207
}

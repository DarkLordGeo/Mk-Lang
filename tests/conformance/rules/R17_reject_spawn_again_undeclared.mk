// verdict: reject E0604
// rule: R17
// note: through a &Scope, spawning again still needs `into s` for every argument: c has none, so each spawn stores an undeclared loan of c into s
Handle(m: &Mutex[int], c: &Chan[int]) {
}

Accept(s: &Scope, m: &Mutex[int] into s, c: &Chan[int]) {
    for _ in 0..3 {
        s.go Handle(m, c) // <- E0604
    }
}

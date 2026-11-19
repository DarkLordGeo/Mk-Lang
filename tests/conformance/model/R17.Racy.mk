// verdict: reject E1002
// case: R17.Racy
// also: H14
Count(c: &Cell[i32]) {
    c.Store(c.Load() + 1)
}

Racy(io: Io) {
    c := Cell[i32].New(0)
    scope s := io.Scope() {
        s.go Count(&c) // <- E1002
    }
}

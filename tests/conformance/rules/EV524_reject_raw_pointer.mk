// verdict: reject E1001
// rule: EV524
// rule: R17
// note: a raw pointer is not `Send`, and it is not a `&C` with `C` not `Share`, so the code is E1001, not E1002
Read(p: *i32) {
}

Spawn(io: Io, p: *i32) {
    scope s := io.Scope() {
        s.go Read(p) // <- E1001
    }
}

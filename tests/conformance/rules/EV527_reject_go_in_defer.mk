// verdict: reject E1003
// rule: EV527
// rule: R17
// note: `s.go` in a `defer` of the scope's own block is E1003
Tick() {
}

Run(io: Io) {
    scope s := io.Scope() {
        defer {
            s.go Tick() // <- E1003
        }
    }
}

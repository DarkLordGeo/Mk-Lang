// verdict: reject E1003
Tick() {
}

Run(io: Io) {
    scope s := io.Scope() {
        defer {
            s.go Tick() // <- E1003
        }
    }
}

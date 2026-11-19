// verdict: reject E1001
// note: a raw pointer is not Send (memory model R17)
Poke(p: *u8) {
}

Run(io: Io, p: *u8) {
    scope s := io.Scope() {
        s.go Poke(p) // <- E1001
    }
}

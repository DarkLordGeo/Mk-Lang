// verdict: ok
// rule: EV524
// rule: R17
// note: `Heap` is `Share`, so `&Heap` crosses a task boundary
Work(h: &Heap) {
}

Spawn(io: Io, h: &Heap) {
    scope s := io.Scope() {
        s.go Work(h)
    }
}

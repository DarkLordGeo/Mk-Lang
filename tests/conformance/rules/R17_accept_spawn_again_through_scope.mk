// verdict: ok
// rule: R17
// note: through a &Scope, a spawn stores its arguments into the scope's own memory and never writes through what an earlier spawn left there, so spawning the same declared parameters again, in a loop, is clean; the scope's node holds the earlier arguments, and they are no store targets of a later spawn (no E0604 c into c)
Handle(m: &Mutex[int], c: &Chan[int]) {
}

Accept(s: &Scope, m: &Mutex[int] into s, c: &Chan[int] into s) {
    for _ in 0..3 {
        s.go Handle(m, c)
    }
}

Twice(s: &Scope, m: &Mutex[int] into s, c: &Chan[int] into s) {
    s.go Handle(m, c)
    s.go Handle(m, c)
}

// verdict: ok
// rule: EV527
// rule: R17
// note: `&s` may be passed to a task
Watch(s: &Scope) {
    s.Cancel()
}

Run(io: Io) {
    scope s := io.Scope() {
        s.go Watch(&s)
    }
}

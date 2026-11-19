// verdict: ok
// note: deep recursion in a task is a run-time matter (a stack-limit panic, `tests/programs/panics/`); statically it compiles
Depth(n: int): int {
    if n == 0 {
        return 0
    }
    return Depth(n - 1) + 1
}

Work() {
    n := Depth(1000000)
    Use(&n)
}

Run(io: Io) {
    scope s := io.Scope() {
        s.go Work()
    }
}

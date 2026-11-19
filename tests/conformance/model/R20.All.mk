// verdict: ok
// case: R20.All
Bump(x: mut i32) {
    *x += 1
}

All(io: Io, xs: mut []i32) {
    scope s := io.Scope() {
        for x in mut xs {
            s.go Bump(x)
        }
    }
}

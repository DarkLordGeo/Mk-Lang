// verdict: ok
// case: H16
Bump(out: mut i32) {
    *out += 1
}

After(io: Io) {
    var n: i32 = 0
    scope s := io.Scope() {
        s.go Bump(mut n)
    }
    Use(&n)
}

// verdict: ok
// case: H32
Read2(x: &i32) {
}

Parent(io: Io) {
    n: i32 = 1
    scope s := io.Scope() {
        s.go Read2(&n)
    }
}

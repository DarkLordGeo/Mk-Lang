// verdict: reject E0504
// case: R17.Peek
// also: H15
Bump(out: mut i32) {
    *out += 1
}

Peek(io: Io) {
    var n: i32 = 0
    scope s := io.Scope() {
        s.go Bump(mut n)
        Use(&n) // <- E0504
    }
}

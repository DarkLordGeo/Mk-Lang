// verdict: ok
// rule: R17
Fill(out: mut i32, v: i32) {
    *out = v
}

Spawn(io: Io): i32 {
    var a: i32 = 0
    var b: i32 = 0
    scope s := io.Scope() {
        s.go Fill(mut a, 1)
        s.go Fill(mut b, 2)
    }
    return a + b
}

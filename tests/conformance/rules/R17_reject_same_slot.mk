// verdict: reject E0501
// rule: R17
// note: each s.go argument's loan lasts until the block exits
Fill(out: mut i32, v: i32) {
    *out = v
}

Twice(io: Io) {
    var n: i32 = 0
    scope s := io.Scope() {
        s.go Fill(mut n, 1)
        s.go Fill(mut n, 2) // <- E0501
    }
}

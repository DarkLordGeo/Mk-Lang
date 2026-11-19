// verdict: reject E0502
// case: R15.Pinned
Pinned(a: &i32, b: &i32) {
    var r := a
    q := &r
    r = b // <- E0502
    Use(q)
}

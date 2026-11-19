// verdict: reject E0604
// case: R17.Helper
Bump(out: mut i32) {
    *out += 1
}

Helper(s: &Scope, x: mut i32) {
    s.go Bump(x) // <- E0604
}

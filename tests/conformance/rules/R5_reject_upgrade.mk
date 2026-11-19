// verdict: reject E0501
// rule: R5
Upgrade() {
    var n := 1
    a := &n
    UseMut(mut n) // <- E0501
    Use(a)
}

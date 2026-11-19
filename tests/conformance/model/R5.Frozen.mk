// verdict: reject E0502
// case: R5.Frozen
Frozen() {
    var n := 1
    a := &n
    n = 2 // <- E0502
    Use(a)
}

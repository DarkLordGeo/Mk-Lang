// verdict: reject E0502
// case: R6.DeferKeepsIt
DeferKeepsIt() {
    var n := 1
    a := &n
    defer Use(a)
    n = 2 // <- E0502
}

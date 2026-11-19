// verdict: ok
// rule: R6
Dead() {
    var n := 1
    a := &n
    b := a
    Use(b)
    n = 2
}

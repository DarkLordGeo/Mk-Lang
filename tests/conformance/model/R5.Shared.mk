// verdict: ok
// case: R5.Shared
Shared() {
    var n := 1
    a := &n
    b := &n
    Use(a)
    Use(b)
    n = 2
}

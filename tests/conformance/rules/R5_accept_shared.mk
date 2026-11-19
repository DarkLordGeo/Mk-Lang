// verdict: ok
// rule: R5
Reads() {
    n := 5
    a := &n
    b := &n
    Use(a)
    Use(b)
    Use(&n)
}

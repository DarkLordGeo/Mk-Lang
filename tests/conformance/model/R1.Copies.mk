// verdict: ok
// case: R1.Copies
Copies() {
    a := Point(x: 1, y: 2)
    b := a
    Use(&a)
    Use(&b)
}

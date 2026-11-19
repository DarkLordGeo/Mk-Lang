// verdict: ok
// rule: R1
// note: an array of plain values is plain, so binding it copies
Arrays() {
    a := [1, 2, 3]
    b := a
    Use(&a)
    Use(&b)
}

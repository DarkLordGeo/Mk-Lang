// verdict: reject E0309
// rule: D-420
// note: a destructured loop variable names fields of the payload record, with the binding rules of (a, b) := e
Sum(xs: [](a: int, b: int)): int {
    var s := 0
    for (a, c) in xs { // <- E0309
        s += a + c
    }
    return s
}

// verdict: reject E0305
// rule: D-420
// note: a destructured loop variable names every field of the payload record, as (a, b) := e does: two of three is refused
Sum(xs: [](a: int, b: int, c: int)): int {
    var s := 0
    for (a, b) in xs { // <- E0305
        s += a + b
    }
    return s
}

// verdict: reject E0101
// rule: D-420
// note: a destructured loop variable iterates by copy only: & before the source is a parse error, as mut is
Sum(xs: [](a: int, b: int)): int {
    var s := 0
    for (a, b) in &xs { // <- E0101
        s += a + b
    }
    return s
}

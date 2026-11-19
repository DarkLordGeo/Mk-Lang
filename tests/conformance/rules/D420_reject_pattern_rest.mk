// verdict: reject E0101
// rule: D-420
// note: a destructured loop variable takes no ..
Sum(xs: [](a: int, b: int)): int {
    var s := 0
    for (a, ..) in xs { // <- E0101
        s += a
    }
    return s
}

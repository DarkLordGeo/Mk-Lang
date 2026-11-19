// verdict: reject E0101
// rule: D-418
// note: an if condition is not a value position
Pick(c: bool, d: bool): int {
    if if c { d } else { false } { // <- E0101
        return 1
    }
    return 0
}

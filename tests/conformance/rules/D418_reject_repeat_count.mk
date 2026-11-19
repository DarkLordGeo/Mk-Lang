// verdict: reject E0101
// rule: D-418
// note: a repeat literal's count is not a value position
Pick(c: bool): int {
    xs := [0; if c { 1 } else { 2 }] // <- E0101
    return xs[0]
}

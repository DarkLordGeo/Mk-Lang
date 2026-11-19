// verdict: reject E0101
// rule: D-418
// note: a repeat literal's value is not a value position: bind the construct to a local first
Pick(c: bool): int {
    xs := [if c { 1 } else { 2 }; 3] // <- E0101
    return xs[0]
}

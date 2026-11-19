// verdict: reject E0101
// rule: D-418
// note: an index is not a value position
Pick(xs: []int, c: bool): int {
    return xs[if c { 0 } else { 1 }] // <- E0101
}

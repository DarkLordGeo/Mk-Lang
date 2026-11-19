// verdict: reject E0311
Pick(c: bool): int {
    x := if c { 1 } // <- E0311
    return x
}

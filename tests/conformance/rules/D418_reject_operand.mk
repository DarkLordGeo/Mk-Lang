// verdict: reject E0101
// rule: D-418
// note: an operand of an operator is not a value position: bind the construct to a local first
Pick(c: bool): int {
    return 1 + if c { 2 } else { 3 } // <- E0101
}

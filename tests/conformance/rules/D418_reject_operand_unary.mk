// verdict: reject E0101
// rule: D-418
// note: the operand of a unary operator is not a value position
Pick(c: bool): int {
    return -if c { 2 } else { 3 } // <- E0101
}

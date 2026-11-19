// verdict: reject E0101
// rule: D-418
// note: a match as the operand of an operator is E0101, like an if: the parser reports it where the value ends, at the operator after its closing brace
Pick(n: int): int {
    return match n {
        0 { 1 }
        _ { 2 }
    } + 1 // <- E0101
}

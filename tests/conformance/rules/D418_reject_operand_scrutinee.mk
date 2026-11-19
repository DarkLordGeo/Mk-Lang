// verdict: reject E0101
// rule: D-418
// note: a match scrutinee is not a value position
Pick(c: bool): int {
    return match if c { 1 } else { 2 } { // <- E0101
        1 { 10 }
        _ { 20 }
    }
}

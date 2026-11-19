// verdict: reject E0101
// rule: D-420
// note: a destructured loop variable iterates by copy only; with mut or & before the source it is a parse error
Zero(xs: mut [](a: int, b: int)) {
    for (a, b) in mut xs { // <- E0101
        _ = a + b
    }
}

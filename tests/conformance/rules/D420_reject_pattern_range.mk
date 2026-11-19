// verdict: reject E0101
// rule: D-420
// note: a destructured loop variable over a range is a parse error
Sum(): int {
    var s := 0
    for (a, b) in 0..3 { // <- E0101
        s += a + b
    }
    return s
}

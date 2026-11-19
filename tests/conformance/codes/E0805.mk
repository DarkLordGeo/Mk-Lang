// verdict: reject E0805
// note: anchored at the closing brace
Sign(n: int): int {
    if n < 0 {
        return -1
    }
} // <- E0805

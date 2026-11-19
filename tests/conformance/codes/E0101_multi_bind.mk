// verdict: reject E0101
// note: foreign form, a, b := F(); hint "write (a, b) := F()"
Two(): (a: int, b: int) {
    return (a: 1, b: 2)
}

Both(): int {
    a, b := Two() // <- E0101
    return a + b
}

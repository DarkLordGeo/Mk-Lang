// verdict: reject E0304
Add(a: int, b: int): int {
    return a + b
}

Three(): int {
    return Add(1) // <- E0304
}

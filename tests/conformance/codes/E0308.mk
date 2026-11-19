// verdict: reject E0308
Both(a: bool, b: bool): bool {
    return a + b // <- E0308
}

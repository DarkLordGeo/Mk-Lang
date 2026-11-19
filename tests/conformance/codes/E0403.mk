// verdict: reject E0403
Pick(c: bool): int {
    y: int
    if c {
        y = 1
    }
    return y // <- E0403
}

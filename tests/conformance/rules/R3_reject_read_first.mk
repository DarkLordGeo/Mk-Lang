// verdict: reject E0403
// rule: R3
Before(): int {
    x: int
    y := x // <- E0403
    x = 1
    return x + y
}

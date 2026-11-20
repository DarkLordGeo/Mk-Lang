// verdict: reject E1208
// note: C's conditional expression names the `if` form
Pick(c: bool, a: int, b: int): int {
    return c ? a : b // <- E1208
}

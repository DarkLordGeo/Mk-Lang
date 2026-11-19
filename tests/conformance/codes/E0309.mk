// verdict: reject E0309
Sum(r: (a: int, b: int)): int {
    (a, c) := r // <- E0309
    return a + c
}

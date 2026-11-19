// verdict: ok
// case: H30
// note: static half only, the stack limit is run time
Deep(n: int): int {
    return Deep(n + 1)
}

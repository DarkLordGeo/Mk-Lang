// verdict: reject E0312
// note: a tail call replaces the frame, so the callee has the caller's signature
Step(n: int): int {
    if n == 0 {
        return 0
    }
    become Add(n, 1) // <- E0312
}

Add(a: int, b: int): int {
    return a + b
}

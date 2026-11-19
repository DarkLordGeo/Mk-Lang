// verdict: reject E0313
// note: Win64 passes a 24-byte struct through memory the caller made, gone after a tail call
struct Big {
    a: int
    b: int
    c: int
}

Sum(b: Big, n: int): int {
    if n == 0 {
        return b.a + b.b + b.c
    }
    next := Big(a: b.a + 1, b: b.b, c: b.c)
    become Sum(next, n - 1) // <- E0313
}

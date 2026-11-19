// verdict: reject E0313
// note: System V passes an 8-byte struct with an unaligned field in memory
packed struct P {
    a: u8
    b: u32
    c: u16
    d: u8
}

F(p: P, n: int): int {
    if n == 0 {
        return Cast[int](p.b)
    }
    become F(P(a: 0, b: p.b + 1, c: 0, d: 0), n - 1) // <- E0313
}

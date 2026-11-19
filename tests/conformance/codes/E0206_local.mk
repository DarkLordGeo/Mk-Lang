// verdict: reject E0206
// note: a local cannot shadow a local
Twice(n: int): int {
    x := n
    if n > 0 {
        x := n * 2 // <- E0206
        return x
    }
    return x
}

// verdict: reject E0708
// note: each level instantiates Nest at [1]T; reported at the outermost concrete call
Nest[T: Plain](x: T, n: int): int {
    if n == 0 {
        return 0
    }
    return Nest([x], n - 1)
}

Start(): int {
    return Nest(1, 3) // <- E0708
}

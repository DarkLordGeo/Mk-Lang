Id[T](x: T): T {
    return x
}

Use(): int {
    f: fn(int): int = Id
    return f(3)
}

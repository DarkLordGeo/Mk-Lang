// verdict: reject E0705
// note: a function with a `const` parameter used as a `fn` value is E0705 at the use: a call through a `fn` value cannot supply a compile-time argument
Twice(const k: int): int {
    return k * 2
}

Use(): int {
    f := Twice // <- E0705
    return f(3)
}

// verdict: reject E0711
Zero[T: Plain](): int {
    return 0
}

Run(): int {
    return Zero() // <- E0711
}

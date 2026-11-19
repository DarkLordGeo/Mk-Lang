// verdict: reject E0804
One(): int {
    return 1
}

Run(): int {
    return try One() // <- E0804
}

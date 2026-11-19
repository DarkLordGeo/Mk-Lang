// verdict: reject E0404
Reset(): int {
    n := 1
    n = 2 // <- E0404
    return n
}

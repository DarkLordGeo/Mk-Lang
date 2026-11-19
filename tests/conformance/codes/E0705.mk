// verdict: reject E0705
Repeat(const n: int): int {
    return n
}

Run(k: int): int {
    return Repeat(k) // <- E0705
}

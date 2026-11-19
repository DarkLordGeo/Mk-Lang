// verdict: reject E0706
Cap(const n: int): int
    requires n > 0
{
    return n
}

Zero(): int {
    return Cap(0) // <- E0706
}

// verdict: reject E0101
// note: foreign form, a ** b; hint "Mk has no power operator", recovered as *
Square(a: int): int {
    return a ** 2 // <- E0101
}

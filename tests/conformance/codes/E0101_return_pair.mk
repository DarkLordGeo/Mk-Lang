// verdict: reject E0101
// note: foreign form, return a, b; return a record instead
Pair(): int {
    return 1, 2 // <- E0101
}

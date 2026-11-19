// verdict: reject E0101
// note: lexical, a stray backslash outside a literal
Sum(): int {
    return 1 \ 2 // <- E0101
}

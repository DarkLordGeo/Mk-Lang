// verdict: reject E0101
// note: lexical, @ is not a Mk token
Limit(): int {
    return @limit // <- E0101
}

// verdict: reject E0101
// note: a decimal part of more than one digit does not start with 0
Seven(): int {
    return 007 // <- E0101
}

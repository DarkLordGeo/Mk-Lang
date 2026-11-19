// verdict: reject E0207
// needs: names
// note: no exact match, one folded match
Helper(): int {
    return 1
}

Call(): int {
    return helper() // <- E0207
}

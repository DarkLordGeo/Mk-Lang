// verdict: reject E0101
// note: foreign form, hint "write var x := e"
Count(): int {
    mut x := 1 // <- E0101
    return x
}

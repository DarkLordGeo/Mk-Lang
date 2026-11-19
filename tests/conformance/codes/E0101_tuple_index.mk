// verdict: reject E0101
// note: foreign form, x.0; records are read by field name
First(p: (a: int, b: int)): int {
    return p.0 // <- E0101
}

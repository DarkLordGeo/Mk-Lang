// verdict: reject E0101
// note: the refused reading of doc/grammar.md: the comma ends the fn type, and *b cannot start a Param
Split(f: fn(a: mut A, b: mut B) *a into b, *b into a) { // <- E0101
}

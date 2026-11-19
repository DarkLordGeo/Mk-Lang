// verdict: reject E0102
// note: a keyword as a local; the use on the next line folds into the declaration's diagnostic
Make(): int {
    type := 1 // <- E0102
    return type
}

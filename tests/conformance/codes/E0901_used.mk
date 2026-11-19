// verdict: reject E0901
// note: a const whose evaluation panics, read by a function body: the E0901 is the one diagnostic; the reader is not an internal error (no panic reaches the user, and one broken item does not hide others)
Div(a: int, b: int): int {
    return a / b
}

const Bad: int = Div(1, 0) // <- E0901

Read(): int {
    return Bad + 1
}

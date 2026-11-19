// verdict: reject E0101
// note: foreign form, --x in prefix position; hint "Mk has no --; - -x with a space negates twice"
Down(x: int): int {
    return --x // <- E0101
}

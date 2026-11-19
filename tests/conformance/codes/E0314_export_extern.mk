// verdict: reject E0314
// note: a bodied export extern is an extern target too
export extern Callback(n: int): int {
    return n
}

Forward(n: int): int {
    become Callback(n) // <- E0314
}

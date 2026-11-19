// verdict: reject E0207
// needs: names
// note: Some folds to the universe name some
Wrap(n: int): ?int {
    return Some(n) // <- E0207
}

// verdict: reject E0101
// note: foreign form, a type followed by <; type arguments use brackets, List[int], and the hit carries facts.target
Count(xs: List<int>): int { // <- E0101
    return xs.Len()
}

// verdict: reject E0101
// note: a value if followed by a postfix suffix is not a value position; bind it to a local first
Pick(c: bool, a: str, b: str): int {
    return if c { a } else { b }.Len() // <- E0101
}

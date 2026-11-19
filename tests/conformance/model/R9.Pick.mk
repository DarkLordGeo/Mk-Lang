// verdict: reject E0602
// case: R9.Pick
Pick(a: str, b: str): str from a {
    return b // <- E0602
}

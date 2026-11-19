// verdict: reject E1210
error Bad

Check(n: int): Bad!int {
    if n < 0 {
        throw Bad // <- E1210
    }
    return n
}

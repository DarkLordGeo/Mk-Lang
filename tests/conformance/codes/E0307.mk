// verdict: reject E0307
Get(n: int): int {
    if x := n { // <- E0307
        return x
    }
    return 0
}

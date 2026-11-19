// verdict: reject E0504
// rule: D-424
// note: the pieces of SplitAt hold the loan their receiver held, so the slice cannot be read while a piece is live
Halves(xs: mut []int): int {
    (left, right) := xs.SplitAt(2)
    n := xs[0] // <- E0504
    left[0] = right[0]
    return n
}

// verdict: reject E0501
// rule: D-424
// note: Swap takes two mut places, and all indices of one array are one place: GetPair or SplitAt gives two
SwapEnds(xs: mut []int) {
    Swap(mut xs[0], mut xs[1]) // <- E0501
}

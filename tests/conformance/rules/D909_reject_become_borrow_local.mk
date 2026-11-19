// verdict: reject E0603
// rule: D-909
// note: no argument of a `become` may hold a loan on a local of the frame it replaces (E0603's become case): the local is gone when the callee runs
Sum(xs: &[]int, n: int): int {
    if n == 0 {
        return xs.Len()
    }
    var local: [3]int = [1, 2, 3]
    become Sum(&local[..3], n - 1) // <- E0603
}

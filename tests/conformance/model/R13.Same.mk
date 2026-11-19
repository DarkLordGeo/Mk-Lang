// verdict: reject E0501
// case: R13.Same
Same() {
    var n := 1
    Swap(mut n, mut n) // <- E0501
}

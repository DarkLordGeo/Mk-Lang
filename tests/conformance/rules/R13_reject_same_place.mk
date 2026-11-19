// verdict: reject E0501
// rule: R13
// rule: R4
// note: all indices of one slice are one place
Swap01(xs: mut []i32) {
    Swap(mut xs[0], mut xs[1]) // <- E0501
}

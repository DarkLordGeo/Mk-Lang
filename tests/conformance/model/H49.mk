// verdict: reject E0501
// case: H49
Bad(xs: mut []i32) {
    for x in mut xs {
        xs.Swap(0, 1) // <- E0501
        *x += 1
    }
}

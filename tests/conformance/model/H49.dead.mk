// verdict: reject E0501
// case: H49.dead
// note: x is dead at the Swap
Bad(xs: mut []i32) {
    for x in mut xs {
        *x += 1
        xs.Swap(0, 1) // <- E0501
    }
}

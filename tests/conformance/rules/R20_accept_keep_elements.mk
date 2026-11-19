// verdict: ok
// rule: R20
// note: each iteration's element loan is separate, so x stays usable across the body while another slice is read
Spread(xs: mut []i32, ys: []i32) {
    for x in mut xs {
        for y in ys {
            *x += y
        }
        UseMut(x)
        UseMut(x)
    }
}

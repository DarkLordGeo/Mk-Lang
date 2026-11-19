// verdict: reject E0501
// case: H67
Twice(xs: mut []i32) {
    a := mut xs[0..]
    b := mut xs[0..] // <- E0501
    a[0] = 1
    b[0] = 2
}

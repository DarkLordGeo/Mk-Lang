// verdict: reject E0501
// case: R4.Indexes
Indexes(xs: mut []i32) {
    a := mut xs[0]
    b := mut xs[1] // <- E0501
    *a += *b
}

// verdict: ok
// rule: R12
Zero(xs: mut []i32) {
    xs[0] = 0
}

Twice(xs: mut []i32) {
    Zero(xs)
    Zero(xs)
}

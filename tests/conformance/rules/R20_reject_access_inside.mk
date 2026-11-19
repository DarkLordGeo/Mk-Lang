// verdict: reject E0504
// rule: R20
// note: the loop holds one exclusive loan on xs until it exits
Inside(xs: mut []int) {
    for x in mut xs {
        *x += xs.Len() // <- E0504
    }
}

// verdict: reject E0101
// note: a float needs a digit on both sides of its dot
Half(): f64 {
    x := .5 // <- E0101
    return x
}

// verdict: reject E0502
// case: H34
Bad() {
    var o: ?i32 = some(1)
    if x := mut o {
        o = none // <- E0502
        *x = 2
    }
}

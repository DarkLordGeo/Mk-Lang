// verdict: reject E0402
// case: R2.Over
Over() {
    var f := Acquire("a")
    f = Acquire("b") // <- E0402
    f.Close()
}

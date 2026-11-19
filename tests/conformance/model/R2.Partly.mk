// verdict: reject E0402
// case: R2.Partly
// also: H27
Partly(c: bool) {
    f := Acquire("a") // <- E0402
    if c {
        f.Close()
    }
}

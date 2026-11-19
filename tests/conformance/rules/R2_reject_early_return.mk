// verdict: reject E0402
// rule: R2
Early(c: bool) {
    r := Acquire("a") // <- E0402
    if c {
        return
    }
    r.Close()
}

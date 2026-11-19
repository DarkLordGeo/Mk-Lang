// verdict: reject E0402
// prelude: yes
// note: _ = e on a linear value (memory model R2)
Drop() {
    _ = Acquire("a") // <- E0402
}

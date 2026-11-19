// verdict: reject E0406
// case: X.E0406
// note: R21: E0406 at the move. The model reported the `defer` line; the marker stays on the move
Bad() {
    f := Acquire("a")
    defer Use(&f)
    f.Close() // <- E0406
}

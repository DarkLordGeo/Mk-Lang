// verdict: reject E0401
// case: R1.Moves
Moves() {
    f := Acquire("a")
    g := f
    f.Close() // <- E0401
    g.Close()
}

// verdict: ok
// case: R2.Reads
Reads() {
    f := Acquire("a")
    defer f.Close()
    Use(&f)
}

// verdict: reject E0402
// note: a conditional move leaves `f` unconsumed on the other path; there are no drop flags
Maybe(c: bool) {
    f := Acquire("f") // <- E0402
    if c {
        Take(f)
    }
}

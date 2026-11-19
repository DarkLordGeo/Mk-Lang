// verdict: reject E0506
// case: R14.TooLong
TooLong() {
    r := &MakePoint() // <- E0506
    Use(r)
}

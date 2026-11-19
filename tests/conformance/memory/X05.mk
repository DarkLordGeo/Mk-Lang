// verdict: reject E0506
// note: a temporary ends at its statement, so a reference to it cannot be kept; bind the temporary to a local
Extend() {
    r := &MakePoint() // <- E0506
    Use(r)
}

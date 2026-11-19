// verdict: reject E0506
// rule: R14
struct PRef {
    p: &Point
}

Keep() {
    h := PRef(p: &MakePoint()) // <- E0506
    Use(&h)
}

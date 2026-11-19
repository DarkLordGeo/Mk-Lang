// verdict: reject E0507
struct Slot {
    n: mut i32
}

Bad(s: &Slot) {
    *s.n = 1 // <- E0507
}

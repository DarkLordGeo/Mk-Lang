// verdict: reject E0507
// case: R12.Bad
// also: H24
struct Sink {
    out: mut []u8
}

Bad(s: &Sink) {
    s.out[0] = 1 // <- E0507
}

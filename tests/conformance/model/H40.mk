// verdict: reject E0409
// case: H40
// note: the two uses keep `a` and `b` used, so no E0208 joins the E0409s
struct Sink {
    out: mut []u8
}

Twice(s: Sink) {
    a := s.out // <- E0409
    b := s.out // <- E0409
    Use(&a)
    Use(&b)
}

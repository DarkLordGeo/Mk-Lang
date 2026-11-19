// verdict: ok
// case: X.FnCover
// note: R9: H(p) covers B(p)
struct Holder {
    name: str
}

ReadOut(h: &Holder): str {
    return h.name
}

Apply(f: fn(h: &Holder): str from h, h: &Holder) {
}

Good(h: &Holder) {
    Apply(ReadOut, h)
}

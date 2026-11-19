// verdict: ok
// case: R8.ReadOut
struct Holder {
    name: str
}

ReadOut(h: &Holder): str {
    return h.name
}

// verdict: reject E0501
// case: R8.StillHolds
struct Holder {
    name: str
}

ReadOut(h: &Holder): str {
    return h.name
}

StillHolds(a: &Arena) {
    var t := Text.New(a)
    h := Holder(name: t.Str())
    n := ReadOut(&h)
    t.Clear() // <- E0501
    Use(&n)
}

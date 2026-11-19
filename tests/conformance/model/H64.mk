// verdict: reject E0602
// case: H64
struct Two {
    a: str
    b: str
}

Two.Pick(t: &Two): str from t.a {
    return t.b // <- E0602
}

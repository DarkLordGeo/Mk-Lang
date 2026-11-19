// verdict: reject E0603
// case: H25
struct W {
    s: str
    n: i32
}

Bad(w: W): &i32 {
    return &w.n // <- E0603
}

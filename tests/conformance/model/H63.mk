// verdict: reject E0607
// case: H63
view struct Words3 {
    text: str
    pos: int
}

Words3.Rest(w: Words3): str from w.text { // <- E0607
    return w.text
}

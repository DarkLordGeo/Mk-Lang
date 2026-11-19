// verdict: reject E0601
// case: R9.Longer
Longer(a: str, b: str): str { // <- E0601
    if a.Len() > b.Len() {
        return a
    }
    return b
}

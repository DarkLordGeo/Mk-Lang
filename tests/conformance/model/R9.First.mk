// verdict: ok
// case: R9.First
First(xs: []i32): &i32 {
    return &xs[0]
}

At(xs: []i32, i: i32): &i32 {
    return &xs[i]
}

Longest(a: str, b: str): str from a, b {
    if a.Len() > b.Len() {
        return a
    }
    return b
}

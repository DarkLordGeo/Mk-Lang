// verdict: reject E0609
// note: closure syntax, reported by the parser
Apply(f: fn(i32): i32): i32 {
    return f(1)
}

Inc(): i32 {
    return Apply(|x| x + 1) // <- E0609
}

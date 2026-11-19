// verdict: reject E0402
// note: an early return with `f` still owned; the rewrite is `defer f.Close()`
Early(c: bool): i32 {
    f := Acquire("f") // <- E0402
    if c {
        return 1
    }
    f.Close()
    return 0
}

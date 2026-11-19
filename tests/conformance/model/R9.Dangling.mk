// verdict: reject E0603
// case: R9.Dangling
Dangling(): &i32 {
    n: i32 = 1
    return &n // <- E0603
}

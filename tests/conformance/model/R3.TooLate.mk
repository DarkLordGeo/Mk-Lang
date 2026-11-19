// verdict: reject E0403
// case: R3.TooLate
TooLate(c: bool): i32 {
    x: i32
    if c {
        x = 1
    }
    return x // <- E0403
}

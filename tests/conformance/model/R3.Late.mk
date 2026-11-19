// verdict: ok
// case: R3.Late
Late(c: bool): i32 {
    x: i32
    if c {
        x = 1
    } else {
        x = 2
    }
    return x
}

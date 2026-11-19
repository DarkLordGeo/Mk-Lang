// verdict: ok
// rule: R3
Pick(n: int): int {
    x: int
    match n {
        0 {
            x = 10
        }
        _ {
            x = 20
        }
    }
    return x
}

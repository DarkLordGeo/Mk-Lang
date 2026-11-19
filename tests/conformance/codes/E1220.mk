// verdict: reject E1220
Sum(n: int): int {
    var s := 0
    for i in 0..=n { // <- E1220
        s += i
    }
    return s
}

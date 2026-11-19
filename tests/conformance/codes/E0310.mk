// verdict: reject E0310
Pick(c: bool): int {
    var y := 0
    x := if c {
        1
    } else {
        y = 2 // <- E0310
    }
    return x + y
}

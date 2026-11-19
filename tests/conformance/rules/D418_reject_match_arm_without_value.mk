// verdict: reject E0310
// rule: D-418
// note: an empty arm, or one whose last line is a loop, has no value
Pick(n: int): int {
    var k := 0
    x := match n {
        0 {} // <- E0310
        _ {
            for k < 3 { // <- E0310
                k += 1
            }
        }
    }
    return x + k
}

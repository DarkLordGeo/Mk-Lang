// verdict: reject E1216
Halve(var n: int): int {
    var steps := 0
    while n > 1 { // <- E1216
        n /= 2
        steps += 1
    }
    return steps
}

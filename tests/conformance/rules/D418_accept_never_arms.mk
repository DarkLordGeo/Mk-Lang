// verdict: ok
// rule: D-418
// note: an arm that does not fall through (return, fail, break, continue) has type never and fits any type
error Bad

Pick(c: bool): Bad!int {
    x := if c { 1 } else { fail Bad }
    return x
}

FirstPositive(xs: []int): int {
    var r := 0
    for x in xs {
        r = if x > 0 { x } else { continue }
        break
    }
    return r
}

Clamp(n: int): int {
    m := match n {
        0 { return 0 }
        _ { n }
    }
    return m
}

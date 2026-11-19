// verdict: reject E0101
// rule: D-420
// note: a destructured loop variable takes no struct path
struct Point {
    x: int
    y: int
}

Sum(ps: []Point): int {
    var s := 0
    for Point(x, y) in ps { // <- E0101
        s += x + y
    }
    return s
}

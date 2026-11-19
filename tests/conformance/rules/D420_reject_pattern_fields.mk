// verdict: reject E0101
// rule: D-420
// note: a destructured loop variable over fields(..) is a parse error
struct Point {
    x: int
    y: int
}

Count(p: Point): int {
    var s := 0
    for (a, b) in fields(p) { // <- E0101
        s += 1
    }
    return s
}

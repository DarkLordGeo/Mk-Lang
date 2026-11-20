// verdict: reject E0101
// note: a struct literal names its fields in parentheses
struct Point {
    x: int
    y: int
}

Origin(): Point {
    return Point{x: 0, y: 0} // <- E0101
}

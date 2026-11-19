// verdict: reject E1213
enum Shape {
    Empty
    Full
}

Unit(): Shape {
    return Shape::Empty // <- E1213
}

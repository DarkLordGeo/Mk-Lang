// verdict: reject E0306
// rule: D-418
// note: a value match must cover every case, as a statement match does
enum Shape {
    Circle(r: f64)
    Rect(w: f64, h: f64)
    Empty
}

Name(s: Shape): str {
    kind := match s { // <- E0306
        .Circle { "circle" }
        .Empty { "empty" }
    }
    return kind
}

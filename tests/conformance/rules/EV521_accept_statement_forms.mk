// verdict: ok
// rule: EV521
// note: `match` on payloads, a range `for`, and record destructuring, which the model never read
enum Shape {
    Dot
    Rect(w: i32, h: i32)
}

struct Pair {
    a: i32
    b: i32
}

Area(s: Shape): i32 {
    match s {
        .Rect(w, h) {
            return w * h
        }
        .Dot {
            return 0
        }
    }
}

Total(shapes: []Shape, p: Pair): i32 {
    Pair(a, b) := p
    var t: i32 = a + b
    for i in 0..shapes.Len() {
        t += Area(shapes[i])
    }
    return t
}

// verdict: ok
// rule: D-418
// note: if, match and else yield a value in every value position: a binding, a typed binding, an assignment, a compound assignment, return, a call argument, a constructor field, an array element, _ = and a value arm's last line
enum Shape {
    Circle(r: f64)
    Rect(w: f64, h: f64)
    Empty
}

struct Pair {
    a: int
    b: int
}

Twice(n: int): int {
    return n * 2
}

Sign(n: i32): i32 {
    return if n < 0 { -1 } else if n == 0 { 0 } else { 1 }
}

Name(s: Shape): str {
    kind := match s {
        .Circle { "circle" }
        .Rect(w, h) {
            if w == h { "square" } else { "rect" }
        }
        .Empty { return "nothing" }
    }
    return kind
}

Positions(c: bool, n: int): int {
    var v := if c { 1 } else { 2 }
    t: int = if c { 3 } else { 4 }
    v = if c { v } else { v + 1 }
    v += match n {
        0 { 10 }
        _ { 20 }
    }
    p := Pair(a: if c { 1 } else { 2 }, b: v)
    r := (x: if c { 7 } else { 8 }, y: 9)
    xs := [if c { 1 } else { 2 }, 3]
    _ = if c { 5 } else { 6 }
    return Twice(if c { p.a } else { p.b }) + xs[0] + t + r.x + r.y
}

Steps(n: int): int {
    total := if n > 10 {
        big := n * 2
        big + 1
    } else {
        n
    }
    return total
}

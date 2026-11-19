// verdict: reject E0603
struct Pos {
    x: i32
    y: i32
}

Local(): &Pos {
    p := Pos(x: 1, y: 2)
    return &p // <- E0603
}

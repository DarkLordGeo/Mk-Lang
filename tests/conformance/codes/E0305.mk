// verdict: reject E0305
struct Pos {
    x: i32
    y: i32
}

Short(): Pos {
    return Pos(x: 1) // <- E0305
}

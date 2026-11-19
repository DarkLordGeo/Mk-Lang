// verdict: reject E0603
// case: H25.Point
Bad(p: Point): &i32 {
    return &p.x // <- E0603
}

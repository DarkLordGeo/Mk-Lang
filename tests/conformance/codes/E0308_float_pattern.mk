// verdict: reject E0308
// note: a float literal pattern
Half(x: f64): int {
    match x {
        0.5 { // <- E0308
            return 1
        }
        _ {
            return 0
        }
    }
}

// verdict: reject E0101
// note: "-" Float is not a pattern; only "-" Int is
Sign(x: int): int {
    match x {
        -1.5 { // <- E0101
            return 0
        }
        _ {
            return 1
        }
    }
}

// verdict: reject E0306
enum Light {
    Red
    Green
    Blue
}

Code(l: Light): int {
    var n := 0
    match l { // <- E0306
        .Red {
            n = 1
        }
        .Green {
            n = 2
        }
    }
    return n
}

// verdict: reject E1214
enum Light {
    Red
    Green
}

Wait(l: Light): int {
    match l {
        .Red => { // <- E1214
            return 1
        }
        .Green {
            return 0
        }
    }
}

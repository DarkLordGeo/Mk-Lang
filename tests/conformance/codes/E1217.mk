// verdict: reject E1217
First(x: ?int): int {
    if let Some(v) = x { // <- E1217
        return v
    }
    return 0
}

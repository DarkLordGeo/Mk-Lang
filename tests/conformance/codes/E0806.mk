// verdict: reject E0806
Stop() {
    defer {
        return // <- E0806
    }
}

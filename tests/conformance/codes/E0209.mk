// verdict: reject E0209
Five(): int {
    return 5
}

Drop() {
    Five() // <- E0209
}

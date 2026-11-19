// verdict: reject E0803
error Bad

Get(): Bad!int {
    return 1
}

Run(): int {
    n := Get() else {} // <- E0803
    return n
}

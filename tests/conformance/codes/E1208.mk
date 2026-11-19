// verdict: reject E1208
error Bad

Parse(): Bad!int {
    return 1
}

Twice(): Bad!int {
    n := Parse()? // <- E1208
    return n * 2
}

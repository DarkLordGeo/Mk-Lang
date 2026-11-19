// verdict: reject E0101
// note: foreign form, range after := in a for header; hint "write for i, x in xs {"
Sum(xs: []int): int {
    var s := 0
    for i, x := range xs { // <- E0101
        s += i + x
    }
    return s
}

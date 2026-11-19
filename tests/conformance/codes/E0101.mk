// verdict: reject E0101
// note: x++ is a foreign form with a hint, "write x += 1"
Bump(var n: int): int {
    n++ // <- E0101
    return n
}

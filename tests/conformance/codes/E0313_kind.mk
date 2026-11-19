// verdict: reject E0313
// note: a niche optional is register-sized but not one of the kinds the tail-call rule lists
Count(p: ?&int, n: int): int {
    if n == 0 {
        return 0
    }
    become Count(none, n - 1) // <- E0313
}

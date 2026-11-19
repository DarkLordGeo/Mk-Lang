// verdict: ok
// rule: R16
struct Bounds {
    lo: int
    hi: int
}

const Range: Bounds = Bounds(lo: 1, hi: 9)

Wide(): int {
    r := &Range
    return r.hi - r.lo
}

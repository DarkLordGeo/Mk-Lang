// verdict: reject E0508
// rule: EV535
// note: without `var`, `mut p` of a by-value parameter is E0508
Bump(p: Point) {
    UseMut(mut p) // <- E0508
}

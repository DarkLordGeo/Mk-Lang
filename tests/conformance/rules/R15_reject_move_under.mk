// verdict: reject E0503
// rule: R15
// note: moving r while a loan under *r is live is still E0503
Hand(r: mut i32) {
    x := &*r
    Take(r) // <- E0503
    Use(x)
}

// verdict: reject E0602
// note: a split by-value parameter starts with every loan-holding field holding p.outer (locals.go splitEntry), so p.b's loans are p's and returning it needs `from p`. Mutant M-RV2-byvalue-entry (only the first field holds p.outer) reads p.b as holding nothing and accepts it: the result would hold the argument's loan undeclared
struct Two {
    a: &i32
    b: &i32
}

F(p: Two, q: &i32): &i32 from q {
    return p.b // <- E0602
}

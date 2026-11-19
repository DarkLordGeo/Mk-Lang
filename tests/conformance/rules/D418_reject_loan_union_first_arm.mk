// verdict: reject E0505
// rule: D-418
// note: a loan of a short-lived local through the first arm outlives it as one through the last arm does (D418_reject_loan_union has it in the last)
Keep(c: bool) {
    long := 1
    var out := &long
    if Cond() {
        short := 2
        out = if c { &short } else { &long } // <- E0505
    }
    Use(out)
}

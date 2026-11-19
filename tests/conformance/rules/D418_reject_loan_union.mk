// verdict: reject E0505
// rule: D-418
// note: the result holds the union of the loans of the yielding arms, so a loan of a short-lived local through one arm outlives it
Keep(c: bool) {
    long := 1
    var out := &long
    if Cond() {
        short := 2
        out = if c { &long } else { &short } // <- E0505
    }
    Use(out)
}

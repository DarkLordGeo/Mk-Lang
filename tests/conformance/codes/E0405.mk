// verdict: reject E0405
// prelude: yes
// note: `r` is unique, not linear, so the loop head's join draws no E0402 beside the E0405 (R2); a linear `r` would add E0402 at its declaration
Again() {
    var n: i32 = 0
    r := mut n
    for {
        Take(r) // <- E0405
    }
}

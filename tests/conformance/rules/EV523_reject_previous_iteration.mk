// verdict: reject E0405
// rule: EV523
// note: the only move of `r` reaches its use around the back edge, so E0405, not E0401; `r` is unique, not linear, so no E0402 joins it
Loop(k: int) {
    var n: i32 = 0
    r := mut n
    for i in 0..k {
        Use(&i)
        Take(r) // <- E0405
    }
}

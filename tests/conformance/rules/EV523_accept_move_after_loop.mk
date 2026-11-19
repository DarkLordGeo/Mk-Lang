// verdict: ok
// rule: EV523
// note: the loop reborrows `r`; the one move is after the loop
Loop(k: int) {
    var n: i32 = 0
    r := mut n
    for i in 0..k {
        Use(&i)
        UseMut(r)
    }
    Take(r)
}

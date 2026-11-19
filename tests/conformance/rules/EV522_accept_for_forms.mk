// verdict: ok
// rule: EV522
// note: `for x in xs` copies a plain element; `for x in &xs` binds `&T` with no new loan
Sum(xs: []i32): i32 {
    var t: i32 = 0
    for x in xs {
        t += x
    }
    return t
}

Each(rs: []Res) {
    for r in &rs {
        Use(r)
    }
}

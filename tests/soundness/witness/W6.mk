// verdict: reject E0505
// note: the call's result reads each field's set from the arguments after the call's stores (`x into p` and `*q into p`), so field r holds what `c into a` stored into a, the loan on y; read before the store it would hold the loan on x only (M-F8-W6-res-before). Anchored at the statement that stores the loan
struct Pair {
    r: &i32
    s: &i32
}

K(c: &i32 into a, a: &i32, b: &i32): Pair from (r: a, s: b) {
    return Pair(r: c, s: b)
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Pair(r: q, ..) := K(&y, &x, &x)
        keep = q // <- E0505
    }
    Use(keep)
}

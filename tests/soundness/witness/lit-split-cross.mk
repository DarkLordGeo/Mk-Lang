// verdict: reject E0505
// note: the literal's field s holds the loan on y, whatever order the literal is written in, so keep cannot take s past y's scope
struct Pair {
    r: &i32
    s: &i32
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Pair(r, s) := Pair(s: &y, r: &x)
        Use(r)
        keep = s // <- E0505
    }
    Use(keep)
}

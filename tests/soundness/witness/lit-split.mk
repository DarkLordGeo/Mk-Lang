// verdict: ok
// note: with no clause anywhere, a struct literal written out of field order into a temporary splits field by field, so r holds the loan on x alone; its split twin is r := &x, s := &y
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
        Use(s)
        keep = r
    }
    Use(keep)
}

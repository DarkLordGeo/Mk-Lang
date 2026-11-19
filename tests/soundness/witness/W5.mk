// verdict: reject E0602
// note: a forwarded per-field call maps the inner clause through its arguments; In(b, a) gives field r the loans of b, which the r of Out may not hold (M-F8-W5-forward-by-name)
struct Pair {
    r: &i32
    s: &i32
}

In(a: &i32, b: &i32): Pair from (r: a, s: b) {
    return Pair(r: a, s: b)
}

Out(a: &i32, b: &i32): Pair from (r: a, s: b) {
    return In(b, a) // <- E0602
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Pair(r, s) := Out(&x, &y)
        Use(s)
        keep = r
    }
    Use(keep)
}

// verdict: ok
// note: precision control: a local bound whole from a per-field call starts split
struct Pair {
    r: &i32
    s: &i32
}

F(a: &i32, b: &i32): Pair from (r: a, s: b) {
    return Pair(r: a, s: b)
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        p := F(&x, &y)
        Use(p.s)
        keep = p.r
    }
    Use(keep)
}

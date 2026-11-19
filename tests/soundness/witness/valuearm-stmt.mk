// verdict: ok
// note: the statement form of valuearm.mk, the `return` in each arm, is checked field by field
struct Pair {
    r: &i32
    s: &i32
}

Pick(c: bool, x: &i32, y: &i32): Pair from (r: x, s: y) {
    if c {
        return Pair(r: x, s: y)
    }
    return Pair(r: x, s: y)
}

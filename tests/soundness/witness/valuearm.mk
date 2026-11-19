// verdict: ok
// note: a join keeps a split temporary split, field by field, so the value-arm local reaches `return` split and the value form checks as the statement form does (valuearm-stmt.mk)
struct Pair {
    r: &i32
    s: &i32
}

Pick(c: bool, x: &i32, y: &i32): Pair from (r: x, s: y) {
    return if c {
        Pair(r: x, s: y)
    } else {
        Pair(r: x, s: y)
    }
}

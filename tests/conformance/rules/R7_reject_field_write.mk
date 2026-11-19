// verdict: reject E0502
// rule: R7
// note: writing a field sets that field's held set (R7), and the whole use `&t` after it keeps every field's loans live, so writing `n` while `t.b` holds its loan fails
struct Two {
    a: &i32
    b: &i32
}

Add() {
    var n: i32 = 1
    m: i32 = 2
    var t := Two(a: &m, b: &m)
    t.b = &n
    n = 3 // <- E0502
    Use(&t)
}

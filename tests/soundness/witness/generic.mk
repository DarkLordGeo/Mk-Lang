// verdict: ok
// note: a generic struct takes a per-field clause, decided before substitution; field a, of type T, is loan-holding; at T = &i32 the destructuring gives a the loan on x alone
struct Two[T] {
    a: T
    b: &i32
}

F[T](x: T, y: &i32): Two[T] from (a: x, b: y) {
    return Two(a: x, b: y)
}

Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Two(a, b) := F(&x, &y)
        Use(b)
        keep = a
    }
    Use(keep)
}

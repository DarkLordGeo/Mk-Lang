// verdict: reject E0608
// note: a generic instance keeps every field source that is still a carrier (R9); at T = &i32, F's field a holds x, which the fn type's a: y does not cover, so Apply's result would hold the loan on x and keep would outlive x
struct S3[T] {
    a: T
    b: &i32
}

F[T](x: T, y: &i32): S3[T] from (a: x, b: y) {
    return S3(a: x, b: y)
}

Apply(g: fn(x: &i32, y: &i32): S3[&i32] from (a: y, b: y), x: &i32, y: &i32): &i32 from y {
    S3(a, b) := g(x, y)
    Use(b)
    return a
}

Caller() {
    y: i32 = 2
    var keep := &y
    if Cond() {
        x: i32 = 1
        keep = Apply(F[&i32], &x, &y) // <- E0608
    }
    Use(keep)
}

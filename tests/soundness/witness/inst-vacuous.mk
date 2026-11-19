// verdict: ok
// note: a generic instance whose field source is no carrier once substituted holds nothing in that field (R9), as the same signature written out does, so F[i32] fits the fn type that names a: x
struct S2[T] {
    a: T
    b: &i32
}

F[T](x: T, y: &i32): S2[T] from (a: x, b: y) {
    return S2(a: x, b: y)
}

Apply(g: fn(x: i32, y: &i32): S2[i32] from (a: x, b: y), y: &i32): &i32 from y {
    S2(a, b) := g(1, y)
    Use(&a)
    return b
}

Caller() {
    y: i32 = 2
    Use(Apply(F[i32], &y))
}

// verdict: reject E0711
// rule: D-254
// note: type arguments are never inferred for a value, in a binding or as an argument
Same[T](x: T): T {
    return x
}

Apply(f: fn(i64): i64, x: i64): i64 {
    return f(x)
}

Bound(): i64 {
    _ = Same // <- E0711
    return 0
}

Passed(): i64 {
    return Apply(Same, 4) // <- E0711
}

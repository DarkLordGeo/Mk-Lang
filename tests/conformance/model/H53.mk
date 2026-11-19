// verdict: reject E0608
// case: H53
Id(x: &i32): &i32 {
    return x
}

Apply[T](f: fn(&i32): T, x: &i32) {
}

Bad(x: &i32) {
    Apply(Id, x) // <- E0608
}

// verdict: reject E0608
// note: Id's result flows from x; fn(&i32): T declares no flow, so Id does not fit it (05 R9, "fn types and interfaces"; the generic result keeps the automatic source B(#1) of fn(&i32): &i32 out)
Id(x: &i32): &i32 {
    return x
}

Apply[T](f: fn(&i32): T): int {
    return 0
}

Run(): int {
    return Apply(Id) // <- E0608
}

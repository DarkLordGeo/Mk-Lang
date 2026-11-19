// verdict: reject E0609
// rule: D-252
// note: a capturing literal is no value: bound to a local, returned, or passed to a parameter that is not const, each at its own literal
Count(xs: []i32, keep: fn(&i32): bool): int {
    var n := 0
    for x in xs {
        if keep(&x) {
            n += 1
        }
    }
    return n
}

Bound(limit: i32) {
    f := fn(x: &i32): bool { return *x > limit } // <- E0609
    _ = f
}

Returned(limit: i32): fn(&i32): bool {
    return fn(x: &i32): bool { return *x > limit } // <- E0609
}

Passed(xs: []i32, limit: i32): int {
    return Count(xs, fn(x: &i32): bool { return *x > limit }) // <- E0609
}

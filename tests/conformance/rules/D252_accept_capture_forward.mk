// verdict: ok
// rule: D-252
// note: a capturing literal passed to a const parameter that forwards it to another const parameter
Count(xs: []i32, const keep: fn(&i32): bool): int {
    var n := 0
    for x in xs {
        if keep(&x) {
            n += 1
        }
    }
    return n
}

CountTwice(xs: []i32, const keep: fn(&i32): bool): int {
    return Count(xs, keep) + Count(xs, keep)
}

Above(xs: []i32, limit: i32): int {
    return CountTwice(xs, fn(x: &i32): bool { return *x > limit })
}

Between(xs: []i32, lo: i32, hi: i32): int {
    return Count(xs, fn(x: &i32): bool { return *x >= lo && *x < hi })
}

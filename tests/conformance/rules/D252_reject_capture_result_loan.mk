// verdict: reject E0609
// rule: D-252
// note: the literal's result holds no loan on a captured name
Pick(xs: []i32, const f: fn(&i32): &i32): int {
    var n := 0
    for x in xs {
        if *f(&x) > 0 {
            n += 1
        }
    }
    return n
}

Leak(xs: []i32, floor: i32): int {
    return Pick(xs, fn(x: &i32): &i32 {
        return &floor // <- E0609
    })
}

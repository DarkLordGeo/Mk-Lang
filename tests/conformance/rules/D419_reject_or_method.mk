// verdict: reject E0207
// rule: D-419
// note: `?T` and `E!T` have no `Or` method; the default is an `else` value block
Find(xs: []int, v: int): ?int {
    for i, x in xs {
        if x == v {
            return some(i)
        }
    }
    return none
}

Pos(xs: []int): int {
    return Find(xs, 7).Or(-1) // <- E0207
}

Kept(xs: []int): int {
    i := Find(xs, 7) else { -1 }
    return i
}

// verdict: reject E0609
// rule: D-252
// note: inside the literal a capture is read through &, so it is not assigned and not borrowed mut
Bump(n: mut int) {
    *n += 1
}

Sorted(xs: mut []i32): int {
    var swaps := 0
    xs.Sort(fn(a: &i32, b: &i32): bool {
        swaps += 1 // <- E0609
        return *a < *b
    })
    return swaps
}

Counted(xs: mut []i32): int {
    var calls := 0
    xs.Sort(fn(a: &i32, b: &i32): bool {
        Bump(mut calls) // <- E0609
        return *a < *b
    })
    return calls
}

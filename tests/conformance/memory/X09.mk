// verdict: reject E1202
// needs: types
// prelude: no
// note: an iterator chain; the rewrite is a `for` loop
Keep(x: &i32): bool {
    return *x > 0
}

Positive(xs: []i32): int {
    ys := xs.filter(Keep) // <- E1202
    return ys.Len()
}

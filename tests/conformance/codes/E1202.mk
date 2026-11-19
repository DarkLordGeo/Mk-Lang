// verdict: reject E1202
// needs: types
Double(x: &i32): i32 {
    return *x * 2
}

All(xs: []i32): int {
    ys := xs.map(Double) // <- E1202
    return ys.Len()
}

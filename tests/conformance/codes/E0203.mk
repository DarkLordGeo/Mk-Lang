// verdict: reject E0203
// needs: types
struct Pair {
    left: i32
    right: i32
}

Middle(p: Pair): i32 {
    return p.middle // <- E0203
}

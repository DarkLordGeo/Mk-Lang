// verdict: reject E0602
// note: a field of a split by-value parameter holds what was written into it, so returning it needs its source
struct Two {
    a: &i32
    b: &i32
}

Keep(var t: Two, y: &i32): &i32 from t {
    t.b = y
    return t.b // <- E0602
}

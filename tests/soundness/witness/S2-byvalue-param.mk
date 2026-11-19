// verdict: ok
// note: a by-value parameter starts split, every loan-holding field holding the parameter's own loan; a field write is strong
struct Two {
    a: &i32
    b: &i32
}

Keep(var t: Two, y: &i32): &i32 from t {
    t.b = y
    Use(t.b)
    return t.a
}

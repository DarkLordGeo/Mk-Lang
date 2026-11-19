// verdict: reject E0608
// case: H21.reject
// note: calls through a fn value are not modelled, only the compatibility check
Put(c: &Cell[&i32], v: &i32 into c) {
    c.Store(v)
}

Apply(f: fn(c: &Cell[&i32], v: &i32), c: &Cell[&i32], v: &i32) {
    f(c, v)
}

Bad(c: &Cell[&i32], v: &i32) {
    Apply(Put, c, v) // <- E0608
}

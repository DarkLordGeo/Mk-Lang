// verdict: ok
// case: H21.accept
// note: a call through `f` stores `v` into `c` as `f`'s type declares, so `Apply` and `Good` declare `v: &i32 into c` (R11, as `Stash`'s E0604)
Put(c: &Cell[&i32], v: &i32 into c) {
    c.Store(v)
}

Apply(f: fn(c: &Cell[&i32], v: &i32 into c), c: &Cell[&i32], v: &i32 into c) {
    f(c, v)
}

Good(c: &Cell[&i32], v: &i32 into c) {
    Apply(Put, c, v)
}

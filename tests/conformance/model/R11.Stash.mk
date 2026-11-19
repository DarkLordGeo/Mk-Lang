// verdict: reject E0604
// case: R11.Stash
struct Holder {
    slot: &Cell[&i32]
}

Put(c: &Cell[&i32], v: &i32 into c) {
    c.Store(v)
}

Stash(h: &Holder, v: &i32) {
    Put(h.slot, v) // <- E0604
}

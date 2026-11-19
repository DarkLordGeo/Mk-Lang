// verdict: reject E0604
// rule: R11
// note: calling a function that stores a parameter is a store, so the clause is restated
struct Slot {
    r: mut &i32
}

Set(s: mut Slot, v: &i32 into s) {
    *s.r = v
}

Forward(s: mut Slot, v: &i32) {
    Set(s, v) // <- E0604
}

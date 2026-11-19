// verdict: reject E0604
struct Slot {
    r: mut &i32
}

Set(s: mut Slot, v: &i32) {
    *s.r = v // <- E0604
}

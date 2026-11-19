// verdict: reject E0604
// case: H18
Store(slot: mut &i32, v: &i32) {
    *slot = v // <- E0604
}

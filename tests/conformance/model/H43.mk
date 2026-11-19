// verdict: reject E0409
// case: H43
Bad(r: &Mutex[i32]) {
    m := *r // <- E0409
    Use(&m)
}

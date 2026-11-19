// verdict: reject E0401
// case: R12.Stored
Stored(r: mut i32) {
    s := r
    UseMut(r) // <- E0401
    UseMut(s)
}

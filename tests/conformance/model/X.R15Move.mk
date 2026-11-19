// verdict: reject E0503
// case: X.R15Move
// note: R15 last sentence
Bad(r: mut i32) {
    q := mut *r
    s := r // <- E0503
    UseMut(q)
    UseMut(s)
}

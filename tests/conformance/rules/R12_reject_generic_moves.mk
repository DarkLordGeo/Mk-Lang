// verdict: reject E0401
// rule: R12
// note: a generic parameter x: T with T = mut i32 moves
Again(r: mut i32) {
    Take(r)
    UseMut(r) // <- E0401
}

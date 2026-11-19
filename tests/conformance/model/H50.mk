// verdict: reject E0701
// case: H50
Bad() {
    var n: i32 = 1
    c := Cell[mut i32].New(mut n) // <- E0701
    Use(&c)
}

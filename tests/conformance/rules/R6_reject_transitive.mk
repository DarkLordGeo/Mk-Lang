// verdict: reject E0502
// rule: R6
// note: g reaches h, which holds the loan on n, so using g keeps it live
struct Ref1 {
    r: &i32
}

Chain() {
    var n: i32 = 1
    h := Ref1(r: &n)
    g := &h
    n = 2 // <- E0502
    Use(g)
}

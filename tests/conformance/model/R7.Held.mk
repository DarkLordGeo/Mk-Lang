// verdict: reject E0502
// case: R7.Held
struct Pair {
    a: &i32
}

Held() {
    var n: i32 = 1
    p := Pair(a: &n)
    n = 2 // <- E0502
    Use(&p)
}

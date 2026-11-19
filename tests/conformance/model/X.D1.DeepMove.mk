// verdict: ok
// case: X.D1.DeepMove
// note: rejected if deep follows the loan graph, see D1
struct V2 {
    a: &i32
    m: mut i32
}

Good(r: mut i32) {
    n: i32 = 1
    y := &n
    h := V2(a: &n, m: r)
    g := h
    Use(y)
    Use(&g)
}

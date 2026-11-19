// verdict: ok
// case: H09
// note: `v` is a local struct, so it holds one set per field (R7); closing `x` ends only `v.a`'s loan, and `v.b` holds the loan on `y` alone. With one set for the whole struct this would be E0503 at `x.Close()`
struct Views {
    a: &Res
    b: &Res
}

Caller() {
    x := Acquire("x")
    y := Acquire("y")
    v := Views(a: &x, b: &y)
    x.Close()
    Use(v.b)
    y.Close()
}

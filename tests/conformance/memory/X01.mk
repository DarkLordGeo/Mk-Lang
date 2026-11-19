// verdict: ok
// note: a local struct holds one set per field (R7): closing `y` ends `v.b`'s loan while only `v.a` is read later, so the struct need not be split by hand. With one set for the whole struct `v` would hold the loans on `x` and `y` together, and this would be E0503 at `y.Close()`. Behind a reference a struct still holds one set
struct Views {
    a: &Res
    b: &Res
}

Split() {
    x := Acquire("x")
    y := Acquire("y")
    v := Views(a: &x, b: &y)
    y.Close()
    Use(v.a)
    x.Close()
}

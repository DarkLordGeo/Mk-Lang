// verdict: ok
// case: H04
// note: `*a into b, *b into a` states that each reference may be stored into the other
// note: named SwapRefs so its body forwards to the prelude `Swap`; a module-level `Swap` hides the prelude's and would call itself
SwapRefs[T](a: mut T, b: mut T) *a into b, *b into a {
    Swap(a, b)
}

Caller() {
    n := 1
    m := 2
    var x := &n
    var y := &m
    SwapRefs(mut x, mut y)
    Use(x)
    Use(y)
}

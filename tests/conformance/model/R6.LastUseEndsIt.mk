// verdict: ok
// case: R6.LastUseEndsIt
LastUseEndsIt() {
    var p := MakePoint()
    a := &p.x
    Use(a)
    p.x = 2
}

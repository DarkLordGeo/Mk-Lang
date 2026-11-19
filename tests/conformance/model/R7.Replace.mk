// verdict: ok
// case: R7.Replace
Replace() {
    var n := 1
    var m := 2
    var r := &n
    r = &m
    n = 3
    Use(r)
}

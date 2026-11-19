// verdict: reject E0401
// rule: D-418
// note: a value arm moves its value as an assignment does, so the source is moved after the construct
Pick(c: bool) {
    r := Acquire("a")
    s := if c {
        r
    } else {
        r.Close()
        Acquire("b")
    }
    s.Close()
    Use(&r) // <- E0401
}

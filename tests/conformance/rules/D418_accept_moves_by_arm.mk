// verdict: ok
// rule: D-418
// note: a value arm moves its value into the hidden local as an assignment does, so a linear value is consumed once on every path
Pick(c: bool) {
    r := Acquire("a")
    s := if c {
        r
    } else {
        r.Close()
        Acquire("b")
    }
    s.Close()
}

// verdict: reject E0502
// rule: D-418
// note: the result holds the loans of every yielding arm, not only the last: a loan through the first arm of an if, or a middle arm of a match, still holds its root
Keep(n: int) {
    var a := 1
    var b := 2
    var d := 3
    x := match n {
        0 { &a }
        1 { &b }
        _ { &d }
    }
    b = 5 // <- E0502
    Use(x)
}

First(c: bool) {
    var a := 1
    var b := 2
    x := if c { &a } else { &b }
    a = 5 // <- E0502
    Use(x)
    Use(&b)
}

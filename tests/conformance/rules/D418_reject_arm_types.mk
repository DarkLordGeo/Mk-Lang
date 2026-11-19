// verdict: reject E0301
// rule: D-418
// note: every yielding arm gives one type; an untyped literal arm adapts, a str does not become an int
Pick(c: bool): str {
    x := if c { 1 } else { "one" } // <- E0301
    return x
}

// verdict: ok
// rule: D-437
// note: with no expected type, the first yielding arm that is not an untyped literal decides, in either arm order
Narrow(c: bool, n: i32): i32 {
    x := if c { 0 } else { n }
    y := if c { n } else { 0 }
    return x + y
}

Default(c: bool): int {
    z := if c { 1 } else { 2 }
    return z
}

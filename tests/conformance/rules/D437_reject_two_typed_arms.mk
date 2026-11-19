// verdict: reject E0301
// rule: D-437
// note: two typed arms of different types do not adapt to each other; the first decides
Pick(c: bool, a: i32, b: i64): i32 {
    x := if c { a } else { b } // <- E0301
    return x
}

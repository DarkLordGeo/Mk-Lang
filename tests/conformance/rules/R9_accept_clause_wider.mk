// verdict: ok
// rule: R9
// note: a clause may name more sources than the body uses, never fewer
Either(a: &i32, b: &i32): &i32 from a, b {
    return a
}

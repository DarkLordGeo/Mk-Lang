// verdict: reject E0602
// note: a record literal is checked field by field as a struct literal is
F(a: &i32, b: &i32): (p: &i32, q: &i32) from (p: a, q: b) {
    return (p: b, q: a) // <- E0602
}

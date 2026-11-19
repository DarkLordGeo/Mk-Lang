// verdict: reject E0602
// note: record-order.mk with the literal crossed; written out of canonical order, the check still pairs each field with its own sources
F(a: &i32, b: &i32): (q: &i32, p: &i32) from (q: b, p: a) {
    return (q: a, p: b) // <- E0602
}

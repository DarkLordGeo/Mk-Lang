// verdict: reject E0505
// note: the store through a loan on t.b lands in field b, the field the loan's place names, not in another (mutant M-F8-S2e-field-first: it lands in field 0)
struct Two {
    a: &i32
    b: &i32
}

Caller() {
    x: i32 = 1
    var t := Two(a: &x, b: &x)
    if Cond() {
        y: i32 = 2
        r := mut t.b
        *r = &y // <- E0505
    }
    Use(t.b)
}

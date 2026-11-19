// verdict: ok
// note: a place rooted at another parameter (`q = &p.a; v := q.b`) knows no field of p, so `p.b = v` moves a loan across fields; R11's first-level exemption covers the store, so the program is clean
struct Q {
    c: &i32
    b: &i32
}

struct P {
    a: Q
    b: &i32
}

F(p: mut P, var q: &Q) {
    q = &p.a
    v := q.b
    p.b = v
}

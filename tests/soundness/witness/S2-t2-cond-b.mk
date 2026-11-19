// verdict: ok
// note: R11's first-level exemption applies to every referent, one with a single loan-holding field included, so a store into *p whole needs no clause
struct One {
    a: &i32
    n: i32
}

F(p: mut One) {
    *p = One(a: p.a, n: 1)
}

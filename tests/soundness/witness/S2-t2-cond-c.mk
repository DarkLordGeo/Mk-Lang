// verdict: ok
// note: a slice referent keeps R11's first-level exemption, as every referent does, so the store needs no clause
F(p: mut []&i32) {
    p[0] = p[1]
}

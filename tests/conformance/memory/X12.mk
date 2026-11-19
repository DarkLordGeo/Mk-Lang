// verdict: reject E0603
// note: the row allows E0503 or E0603; this is the E0603 form. Three depths reach `h.a` through one reference: the parameter `p`, the local `n`, and `t`, which `h` borrows. A value read through a reference holds what its target holds, all of it (R8): a struct behind a reference has one set, so `h.a` also holds the loan on `n`; the rewrite is to copy `p` out, or hold a local reference. A struct in a local read field by field is accepted, since a local struct holds one set per field (rules/D567_accept_x12_local_struct.mk)
struct Two {
    a: &i32
    b: &i32
}

Pick(p: &i32): &i32 {
    n: i32 = 1
    t := Two(a: p, b: &n)
    h := &t
    Use(h.b)
    return h.a // <- E0603
}

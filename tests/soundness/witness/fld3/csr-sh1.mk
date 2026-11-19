// verdict: reject E0505
// note: the self-reach check's walk reads no shallow mark: Put stores a shallow &i32 of u into t, and Keep stores S(a: mut t, b: &n) into h. The self-reach check's walk from S reaches t, then u through t's shallow loan, and u, which holds a Cell inline, takes n's loan: E0505 when n dies. A walk that skips the shallow loan checks this clean. Witness of M-F3-csr-marks
// before FLD-3: reject E0505 at 36 E0505 (measured by review-borrow's calls lens on m9-item2-r 5ac7bcaf, mk check, 2026-11-15)
struct U {
    c: Cell[?&i32]
    x: i32
}

struct T {
    r: &i32
}

Put(t: mut T, r: &i32 into t) {
    t.r = r
}

struct S {
    a: mut T
    b: &i32
}

struct H {
    s: ?S
}

Keep(h: mut H, x: S into h) {
    h.s = some(x)
}

Inner(u: &U) {
    dummy: i32 = 0
    var t := T(r: &dummy)
    Put(mut t, &u.x)
    n: i32 = 7
    var h := H(s: none)
    Keep(mut h, S(a: mut t, b: &n)) // <- E0505
}

UseU(u: &U): i32 {
    Inner(u)
    var v: i32 = -1
    if r := u.c.Load() {
        v = *r
    }
    return v
}

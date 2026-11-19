// verdict: reject E0505
// note: Get(s: mut S): mut ?&i32 returns `mut *s.m`, so its result holds the loan of
// the plain local sv, one level above the caller's x. sv holds a mut reference, so a direct write through the
// result walks on from it to x: E0505. If the walk stopped at sv, the write would never reach x, and a run
// would read a dead frame. Mutant M-D790-walk
// accepts it
// The mut-field form of P5-CR1-x01fromp, with no interior mutability.
// S holds `m: mut ?&i32` (own bytes by R10, so Get's automatic source is
// H(s)); Get returns mut *s.m, a reference into the caller's x, which S
// only holds. Stash writes a reference to its own local n through Get's
// result (a direct write); if the walk stops at the plain local sv, x keeps
// &n after Stash returns. Run then reads *x: a value other than 5 is garbage
// from Stash's dead frame.
struct S {
    m: mut ?&i32
}

Get(s: mut S): mut ?&i32 {
    return mut *s.m
}

Stash(x: mut ?&i32): int {
    var sv := S(m: x)
    p := Get(mut sv)
    var n: i32 = 5
    *p = some(&n) // <- E0505
    return 0
}

Clobber(v: i32): i32 {
    var buf: [256]i32 = [v; 256]
    var s: i32 = 0
    for i in 0..256 {
        buf[i] = buf[i] +% Trunc[i32](i)
        s = s +% buf[i]
    }
    return s
}

Run(): int {
    var x: ?&i32 = none
    if Stash(mut x) != 0 {
        return -2
    }
    var before: i32 = -1
    if r := x {
        before = *r
    }
    _ = Clobber(1000000)
    var after: i32 = -1
    if r := x {
        after = *r
    }
    return Cast[int](before) * 10000000 + Cast[int](after)
}

export Main(sys: &System): WriteError!void {
    r := Run()
    try sys.Print("result {r}\n", (r))
}

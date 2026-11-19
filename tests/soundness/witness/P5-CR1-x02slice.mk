// verdict: reject E0505
// note: P5-CR1-x02mutfield with a `mut []?&i32` field: an index write through Get's result must reach the caller's
// array, not only the plain local sv: E0505. Mutant M-D790-walk accepts it,
// and a run then reads a dead frame
// P5-CR1-x02mutfield with a `mut []?&i32` field. Get's automatic source
// is H(s) (a mut reference at any depth, R10 row 2), so p holds the loan of
// the plain local sv; the index write p[0] = some(&n) lands on sv, not on the
// caller's array. Run reads arr[0] after Stash returned and the stack was
// clobbered: before*10^7 + after, any after other than 5 is the dead frame.
struct S {
    xs: mut []?&i32
}

Get(s: mut S): mut []?&i32 {
    return mut s.xs[..]
}

Stash(xs: mut []?&i32): int {
    var sv := S(xs: xs)
    p := Get(mut sv)
    var n: i32 = 5
    p[0] = some(&n) // <- E0505
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
    var arr: [2]?&i32 = [none; 2]
    if Stash(mut arr[..]) != 0 {
        return -2
    }
    var before: i32 = -1
    if r := arr[0] {
        before = *r
    }
    _ = Clobber(1000000)
    var after: i32 = -1
    if r := arr[0] {
        after = *r
    }
    return Cast[int](before) * 10000000 + Cast[int](after)
}

export Main(sys: &System): WriteError!void {
    r := Run()
    try sys.Print("result {r}\n", (r))
}

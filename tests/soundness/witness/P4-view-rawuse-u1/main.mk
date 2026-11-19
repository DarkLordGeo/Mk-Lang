// verdict: reject E0502
// note: a whole key on a view's field that holds no loans: the only use of h after n is written is h.p, a raw pointer. On a view's split shape a key on such a field is a whole key, so the use keeps the rest, which holds n's loan, live, and the write to n is E0502. With the key left on the field (mutant M-P4-view-rawkey) the use keeps only p's empty set live, the program checks clean, and `mk run` prints `v=4242`: a read through the loan the literal took on n, after n was written while that loan was live. This package's pkg.mk lists `.` in unsafe_modules
import std.raw

view struct H {
    p: *i32
    tag: &i32
}

Inner(t: &i32): i32 {
    var n: i32 = 7
    var h := H(p: raw.Of(&n), tag: &n)
    h.tag = t
    n = 4242 // <- E0502
    unsafe "witness: read the raw storage through the field" {
        return raw.Read(h.p)
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    t: i32 = 1
    v := Inner(&t)
    try Print(mut out, "v={v}\n", (v))
}

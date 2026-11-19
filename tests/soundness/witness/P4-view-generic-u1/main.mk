// verdict: reject E0502
// note: a view's split shape on an instance: G[T]'s field a mentions T, so it is loan-holding as written (check), but at G[*i32] it holds no loans as typed (borrow judges the instance), so a key on g.a is a whole key and the use keeps the rest, which holds n's loan, live: E0502 at the write to n. With the key left on the field (mutant M-P4-view-rawkey) the program checks clean and `mk run` prints `v=4242` where the value under the loan was 7. This package's pkg.mk lists `.` in unsafe_modules
import std.raw

view struct G[T] {
    a: T
    b: &i32
}

Inner(t: &i32): i32 {
    var n: i32 = 7
    var g := G[*i32](a: raw.Of(&n), b: &n)
    g.b = t
    n = 4242 // <- E0502
    unsafe "witness: read the raw storage through the field" {
        return raw.Read(g.a)
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    t: i32 = 1
    v := Inner(&t)
    try Print(mut out, "v={v}\n", (v))
}

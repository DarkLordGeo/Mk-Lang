// verdict: reject E0505
// note: the self-reach check's own witness: D612-selfreach-sr2's shape with F's self-reach write done through a raw pointer, the idiom of core's Replace (std/core/mem_release.mk), so no body judges it. F stores x into h and then writes x.b through h.s.a. That exceeds its signature: no signature declares that write beside `x into h`, and a judged F doing it is E0604 (sr2, sr3), so this F breaks its unsafe contract (R19). The self-reach check (a stored loan never reaches its own `mut` referent), a second line of defence, rejects the direct caller Inner. It does not reject a caller that forwards its own parameter. With the check cut (mutant M-CSR) the program checks clean and `mk run` prints `keep=0 r=12069 k=12069`, r reading Inner's dead n. This package's pkg.mk lists `.` in unsafe_modules
import std.raw

struct S {
    a: mut &i32
    b: &i32
}

struct H {
    s: S
}

F(h: mut H, x: S into h) {
    v := x.b
    h.s = x
    q := raw.OfMut(mut *h.s.a)
    unsafe "witness: this write of x's loan through the stored x exceeds `x into h`, so F breaks its contract on purpose" {
        raw.Write(q, v)
    }
}

Inner(p: mut &i32) {
    n: i32 = 7
    dummy: i32 = 0
    var dr: &i32 = &dummy
    var h := H(s: S(a: mut dr, b: &dummy))
    F(mut h, S(a: p, b: &n)) // <- E0505
}

Clobber(n: i32): i32 {
    var acc: i32 = n
    var w0: i32 = n + 1001
    var w1: i32 = n + 2002
    for _ in 0..4 {
        acc = acc + w0 + w1
        w0 = w0 + 1
        w1 = w1 + 1
    }
    return acc
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    keep: i32 = 0
    var r: &i32 = &keep
    Inner(mut r)
    k := Clobber(5)
    v := *r
    try Print(mut out, "keep={keep} r={v} k={k}\n", (keep, v, k))
}

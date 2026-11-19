// verdict: reject E0505
// note: the whole-body fallback's own witness: FR38D-E1-copy with F's write done through a raw pointer, the idiom of core's Replace (std/core/mem_release.mk), so no body judges it, as in CSR-unsafe-u1. Outer forwards its by-value x to F, which stores x into h and writes x.b through h.s.a; the self-reach check in Outer drops x's own loan at x's node, so no check sees that store. Main's decoration (pp.s = &n, then pp.t = r) makes one loan set per local reject the program by accident; field sets take n out of the field read later, so only the fallback, which checks Main whole, rejects it. F does what no judged body with its signature may do, which R19 forbids, so the fallback is a second line of defence, as the self-reach check is. With the fallback cut (mutant M-FIX12-remnant-whole) the program checks clean and `mk run` reads the dead local n. This package's pkg.mk lists `.` in unsafe_modules
import std.raw

struct S {
    a: mut &i32
    b: &i32
}

struct Hd {
    s: S
}

struct TT {
    t: &i32
    s: &i32
}

F(l: mut Hd, x: S into l) {
    v := x.b
    l.s = x
    q := raw.OfMut(mut *l.s.a)
    unsafe "l.s.a is a live mut reference to an initialized &i32; v moves in, and `x into l` declares the store" {
        raw.Write(q, v)
    }
}

Outer(x: S) {
    dummy: i32 = 0
    var dr: &i32 = &dummy
    var h := Hd(s: S(a: mut dr, b: &dummy))
    F(mut h, x)
}

Clobber(n: i32): i32 {
    var acc: i32 = n
    var w0: i32 = n + 1001
    var w1: i32 = n + 2002
    var w2: i32 = n + 3003
    var w3: i32 = n + 4004
    for _ in 0..4 {
        acc = acc + w0 + w1 + w2 + w3
        w0 = w0 + 1
        w1 = w1 + 1
        w2 = w2 + 1
        w3 = w3 + 1
    }
    return acc
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    keep: i32 = 0
    var r: &i32 = &keep
    x0: i32 = 0
    var pp := TT(t: &x0, s: &x0)
    if sys.args.Len() >= 0 {
        n: i32 = 7
        pp.s = &n // <- E0505
        Outer(S(a: mut r, b: &n))
        pp.t = r
    }
    k := Clobber(5)
    w := pp.t
    v := *w
    try Print(mut out, "keep={keep} r={v} k={k}\n", (keep, v, k))
}

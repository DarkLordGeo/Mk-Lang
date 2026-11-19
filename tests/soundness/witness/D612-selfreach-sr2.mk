// verdict: reject E0505 E0604
// note: self-reach: a value stored into a holder brings a loan whose own `mut &` referent the callee then writes through the holder; the stored loans reach their own `mut` referents, so the program is rejected (a struct holder, no arena). Accepted, it reads a dead local when run. Acceptance means the hole reopened. F's own check also rejects it (E0604 at the write), so cutting the self-reach check (a stored loan never reaches its own `mut` referent) alone (mutant M-CSR) does not reopen it; that check stays as a second line of defence, witnessed by CSR-unsafe-u1 and CSR-extern-e1, whose F exceeds its signature in unsafe or extern code. Self-reach through a forwarding parameter is witnessed by FH-selfreach-r3
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
    *h.s.a = v // <- E0604
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

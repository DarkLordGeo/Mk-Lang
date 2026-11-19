// verdict: reject E0505
// note: the type-parameter clause of the direct write's walk on. A generic Stash[T] wraps its T (at T = mut ?&i32, the caller's slot) in a local S[T] and writes a local's reference through Get's result, one level above the slot. The walk on from the local sv, whose type holds a type parameter, lands the store on what sv holds, the caller's slot: E0505. Without it the write lands on sv only, and a dev run prints `slot 1515870810` (a dead read). The ownInline exemption covers parameters only, so sv keeps walking on. Mutant M-D790-walk-param, which cuts the clause for a local, accepts it
struct S[T] {
    m: T
}

Get[T](s: mut S[T]): mut ?&i32 from s {
    if T is mut ?&i32 {
        return mut *s.m
    }
    Panic("not reached")
}

Sink(x: mut ?&i32) {
    _ = x
}

Stash[T](x: T, sink: fn(x: T)): int {
    var sv := S[T](m: x)
    p := Get[T](mut sv)
    var n: i32 = 5
    *p = some(&n) // <- E0505
    S(m) := sv
    sink(m)
    return 0
}

Paint(depth: int, p: &i32): i32 {
    var x: [64]i32 = [*p; 64]
    if depth == 0 {
        return x[3]
    }
    return Paint(depth - 1, &x[1]) +% x[2]
}

export Main(sys: &System): WriteError!void {
    var slot: ?&i32 = none
    _ = Stash[mut ?&i32](mut slot, Sink)
    var c: i32 = 1515870810
    _ = Paint(8, &c)
    var v: i32 = -1
    if r := slot {
        v = *r
    }
    try sys.Print("slot {v} (a live value would be 5)\n", (v))
}

// verdict: reject E0602
// note: G756-gen-sfn as `Launder[T](f: fn(k: &i32): T from k, k: &i32): T from k`. E0602 at the return. If accepted, a dev build prints `before 5 after 1515870810`. Acceptance means the hole reopened (mutant M-D756-gen-sfn)
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

GetK(k: &i32): &Mutex[?&i32] {
    _ = k
    return &GM
}

Launder[T](f: fn(k: &i32): T from k, k: &i32): T from k {
    return f(k) // <- E0602
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    k: i32 = 0
    m := Launder[&Mutex[?&i32]](GetK, &k)
    var n: i32 = 5
    var g := m.Lock(io) else {
        return -2
    }
    g.Store(some(&n))
    g.Unlock()
    return 0
}

Paint(depth: int, p: &i32): i32 {
    var x: [64]i32 = [*p; 64]
    if depth == 0 {
        return x[3]
    }
    return Paint(depth - 1, &x[1]) +% x[2]
}

Read(io: Io): i32 {
    var g := GM.Lock(io) else {
        return -1
    }
    var v: i32 = -1
    if r := *g.Ref() {
        v = *r
    }
    g.Unlock()
    return v
}

export Main(sys: &System): WriteError!void {
    if Stash(sys.io, 3) == -1000 {
        return
    }
    before := Read(sys.io)
    var c: i32 = 1515870810
    _ = Paint(8, &c)
    after := Read(sys.io)
    try sys.Print("before {before} after {after}\n", (before, after))
}

// verdict: reject E0604
// note: a fn value's `&T` stored into an out-parameter in a generic body. Fill[T](out: mut ?&T, f: fn(): &T) writes f()'s result, the global node, into *out, whose type `?&T` takes no store as written, so a check of the type as written does not see it; at T = Mutex[?&i32] with Get behind f, the caller's m would hold nothing and a local stored through its guard would reach GM. A parameter node's takes is judged for every instance (setup.go addAbstract, storesFirstGen): E0604 at the store. If accepted, a dev build prints `before 5 after 1515870810` (a dead frame). Acceptance means the hole reopened (mutant M-D756-gen-out)
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

Get(): &Mutex[?&i32] {
    return &GM
}

Fill[T](out: mut ?&T, f: fn(): &T) {
    *out = some(f()) // <- E0604
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var m: ?&Mutex[?&i32] = none
    Fill[Mutex[?&i32]](mut m, Get)
    var n: i32 = 5
    if r := m {
        var g := r.Lock(io) else {
            return -2
        }
        g.Store(some(&n))
        g.Unlock()
    }
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

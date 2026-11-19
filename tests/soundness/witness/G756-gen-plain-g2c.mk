// verdict: reject E0602
// note: a type parameter with a Plain bound: G756-gen-plain-call with a `const` fn parameter, `Call[T: Plain](const f: fn(): T): T`. Plain is a copy kind, not 'holds no loan': &T is Plain, so T: Plain admits &Mutex[?&i32]. E0602 at the return. An exemption of Plain type parameters (`!w.plain(t)` in firstTakes) would accept it, and the global would read a dead frame in dev and release. Mutant M-D756-gen-plain, which restores that exemption, accepts it
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

Get(): &Mutex[?&i32] {
    return &GM
}

Call[T: Plain](const f: fn(): T): T {
    return f() // <- E0602
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    m := Call[&Mutex[?&i32]](Get)
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

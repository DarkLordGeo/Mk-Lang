// verdict: reject E0602
// note: a result that names a source (d, a local Mutex at the caller) but returns the global, which takes stores; the caller would judge the store of &n against lm, which n outlives. The boundary refuses a static that takes stores in a sourced result: E0602. If accepted, `mk run` reads a dead frame (canary 1515870810 after Paint, dev and release). Mutant M-D756-boundary accepts it
// A result that has a source (a local Mutex of the same type), but the body returns the global.
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

Get4(d: &Mutex[?&i32]): &Mutex[?&i32] {
    return &GM // <- E0602
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    var lm := Mutex[?&i32].New(none)
    m := Get4(&lm)
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

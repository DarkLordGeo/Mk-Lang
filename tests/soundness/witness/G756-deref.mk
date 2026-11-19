// verdict: reject E0605
// note: a reference read out of global memory (GR) that a source-less call handed back, through the guard's Ref, is still global memory, so storing a local through it is E0605. Accepted, `mk run --release` reads a dead frame (canary 1515870810). Acceptance means the hole reopened (mutant M-D756-result)
// A reference read out of global memory that a source-less call handed back is still global memory.
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)
var GR: Mutex[?&Mutex[?&i32]] = Mutex[?&Mutex[?&i32]].New(none)

Publish(io: Io): int {
    var g := GR.Lock(io) else {
        return -1
    }
    g.Store(some(&GM))
    g.Unlock()
    return 0
}

GetR(): &Mutex[?&Mutex[?&i32]] {
    return &GR
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    if Publish(io) != 0 {
        return -1
    }
    r := GetR()
    var g := r.Lock(io) else {
        return -1
    }
    var n: i32 = 5
    if m := *g.Ref() {
        var g2 := m.Lock(io) else {
            g.Unlock()
            return -2
        }
        g2.Store(some(&n)) // <- E0605
        g2.Unlock()
    }
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

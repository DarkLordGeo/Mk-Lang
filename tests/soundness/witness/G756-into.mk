// verdict: reject E0605
// note: the source-less result passed straight to a call whose clause stores x into it; the call's store walk reaches the global node: E0605. If accepted, `mk run` reads a dead frame (canary 1515870810 after Paint, dev and release). Mutant M-D756-result accepts it
// The source-less result passed straight to a call whose clause stores into it.
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

Get(): &Mutex[?&i32] {
    return &GM
}

Put(m: &Mutex[?&i32], x: &i32 into m, io: Io): int {
    var g := m.Lock(io) else {
        return -2
    }
    g.Store(some(x))
    g.Unlock()
    return 0
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    return Put(Get(), &n, io) // <- E0605
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

// verdict: reject E0602
// note: Get6 names a source but returns what a source-less call handed back (the global node), and GetR, with a raw-pointer parameter, returns the global (R19: its callers take the result as made from the pointer). Both are refused at the return (E0602). If accepted, `mk run` reads a dead frame (canary 1515870810 after Paint, dev and release). Mutant M-D756-boundary accepts it
// A result with a real source returns what a source-less call handed back; and a raw-pointer parameter's result returns the global.
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

Get(): &Mutex[?&i32] {
    return &GM
}

Get6(d: &Mutex[?&i32]): &Mutex[?&i32] {
    _ = d
    return Get() // <- E0602
}

GetR(p: *i32): &Mutex[?&i32] {
    return &GM // <- E0602
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    var lm := Mutex[?&i32].New(none)
    m := Get6(&lm)
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

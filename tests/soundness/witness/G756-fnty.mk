// verdict: reject E0605
// note: a function whose result names no source, given as a fn type whose result is from x; the store must not be judged against z alone. A fn value call's carrier result also holds the global node's loan, so the store is E0605. If accepted, `mk run` reads a dead frame (canary 1515870810 after Paint, dev and release). Mutant M-D756-indirect accepts it
// A function whose result names no source, given as a fn type whose result names one.
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

GetX(d: &Mutex[?&i32]): &Mutex[?&i32] from d {
    return d
}

GetY(x: &i32): &Mutex[?&i32] {
    _ = *x
    return &GM
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    var lm := Mutex[?&i32].New(none)
    f: fn(d: &Mutex[?&i32]): &Mutex[?&i32] from d = GetX
    _ = f(&lm)
    var z: i32 = 0
    h: fn(x: &i32): &Mutex[?&i32] from x = GetY
    m := h(&z)
    var g := m.Lock(io) else {
        return -2
    }
    g.Store(some(&n)) // <- E0605
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

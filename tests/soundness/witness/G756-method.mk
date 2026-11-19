// verdict: reject E0605
// note: a method whose receiver cannot supply the result (K holds no Mutex), so the result names no source: E0605 at the store. If accepted, `mk run` reads a dead frame (canary 1515870810 after Paint, dev and release). Mutant M-D756-result accepts it
// A method whose receiver cannot supply the result (no source).
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

struct K {
    k: int
}

K.Get(&self): &Mutex[?&i32] {
    return &GM
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    kk := K(k: 1)
    m := kk.Get()
    var n: i32 = 5
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

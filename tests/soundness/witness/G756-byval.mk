// verdict: reject E0605
// note: the loan-free struct passed through a by-value identity function keeps the global node's loan (H(w)). Accepted, `mk run` reads a dead frame (canary 1515870810 after Paint, dev and release). Acceptance means the hole reopened (mutant M-D756-result)
// The loan-free struct passed through a by-value identity function.
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

struct W {
    m: &Mutex[?&i32]
    k: int
}

MkW(): W {
    return W(m: &GM, k: 1)
}

Id(w: W): W {
    return w
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    w := Id(MkW())
    var n: i32 = 5
    var g := w.m.Lock(io) else {
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

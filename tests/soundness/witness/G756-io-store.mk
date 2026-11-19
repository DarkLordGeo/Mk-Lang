// verdict: reject E0605
// note: a source-less function taking Io: LockO(io: Io): ?Guard[?&i32] hands back GM's guard. Io does not count as a raw pointer (holdsRaw), so LockO's result holds the global node at its callers, and the store of a local's loan through the guard is E0605. Mutant M-D756-result, which cuts the caller's half, accepts it
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

LockO(io: Io): ?Guard[?&i32] {
    g := GM.Lock(io) else {
        return none
    }
    return some(g)
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    var g := LockO(io) else {
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

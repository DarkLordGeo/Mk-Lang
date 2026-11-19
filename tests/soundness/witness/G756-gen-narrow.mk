// verdict: reject E0602
// note: a source-less global through a narrowed branch: Pick[T](d: &T): &T returns Get()'s global under `if T is Mutex[?&i32]`, under its elided source d. Judged on the result's type as written, &T, the global node would pass; it is judged for every instance (storesFirstGen): E0602. If accepted, a run reads a dead frame. Returning &GM directly in that branch is E0602 as well. Mutant M-D756-gen-boundary accepts it
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

Get(): &Mutex[?&i32] {
    return &GM
}

Pick[T](d: &T): &T {
    if T is Mutex[?&i32] {
        return Get() // <- E0602
    }
    return d
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    var lm := Mutex[?&i32].New(none)
    m := Pick[Mutex[?&i32]](&lm)
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

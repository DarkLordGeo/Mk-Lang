// verdict: reject E0604
// note: a source-less global through an out-parameter: GetInto(out: mut ?&Mutex[?&i32]) writes &GM into *out. If a static loan stored into a parameter's node needed no clause, the caller's m would hold nothing after the call and a local stored through it would reach GM. A static of global memory that takes stores, stored into a node whose type may point into such memory, is E0604 at the callee. If accepted, a run reads a dead frame. Mutant M-D756-outparam accepts it
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

GetInto(out: mut ?&Mutex[?&i32]) {
    *out = some(&GM) // <- E0604
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var m: ?&Mutex[?&i32] = none
    GetInto(mut m)
    var n: i32 = 5
    if r := m {
        var g := r.Lock(io) else {
            return -2
        }
        g.Store(some(&n))
        g.Unlock()
    }
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

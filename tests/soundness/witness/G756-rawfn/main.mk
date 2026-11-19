// verdict: reject E0605
// note: the raw-pointer exemption of a source-less result applies to a direct callee only: G[T: Plain](x: T): &Mutex[?&i32] returns &GM with no source and passes its boundary, since T holds no raw pointer. Given as fn(*i32), whose parameter does hold one, its result still holds the global node at the caller (R19's exemption is not read off the fn type), so a local stored through it is E0605. If accepted, `mk run` prints `before 5 after 1515870810` in dev and release, a dead read. Mutant M-D756-rawdirect accepts it. This package's pkg.mk lists `.` in unsafe_modules, for std.raw
import std.raw
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

G[T: Plain](x: T): &Mutex[?&i32] {
    return &GM
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    f: fn(*i32): &Mutex[?&i32] = G[*i32]
    m := f(raw.Null[i32]())
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

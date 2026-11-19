// verdict: reject E0605
// note: a reference read straight through a reference that holds a static loan keeps that loan, though a static node holds nothing. p from GO.Get of a global sync.Once[&Mutex[?&i32]] holds GO's static loan; if m := *p held nothing, a local stored through m's guard would land nowhere. B keeps a static loan of a carrier static node: E0605 at the store. If accepted, a release build prints `before 5 after 1515870810` (a dead frame; dev shows no change). Acceptance means the hole reopened (mutant M-N2-static)
// G756-deep without the source-less wrapper (a control): Stash reads GO.Get's
// `&&Mutex[?&i32]` itself and stores a local through *p.
import std.sync

var GM: Mutex[?&i32] = Mutex[?&i32].New(none)
var GO: sync.Once[&Mutex[?&i32]] = sync.Once[&Mutex[?&i32]].New()

GetM(): &Mutex[?&i32] {
    return &GM
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    p := GO.Get(io, GetM) else {
        return -3
    }
    m := *p
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

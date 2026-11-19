// verdict: reject E0605
// note: a second level written at run time. A global sync.Once[&Mutex[?&i32]] is Interior, and OncePut writes its &Mutex at run time; a source-less GetP(io): ?&&Mutex[?&i32] hands back GO.Get's result, whose first level (`&Mutex[?&i32]`) takes no store by type. If only the first level counted, the caller's p would hold nothing, and a local stored through `(*p).Lock(io)` would reach GM. Every level counts (typewalk.go firstTakes), and `*p` keeps the global node's loan: E0605 at the store. If accepted, dev and release builds print `before 5 after 1515870810`. Acceptance means the hole reopened (mutant M-D756-deep)
import std.sync

var GM: Mutex[?&i32] = Mutex[?&i32].New(none)
var GO: sync.Once[&Mutex[?&i32]] = sync.Once[&Mutex[?&i32]].New()

GetM(): &Mutex[?&i32] {
    return &GM
}

GetP(io: Io): ?&&Mutex[?&i32] {
    p := GO.Get(io, GetM) else {
        return none
    }
    return some(p)
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    if p := GetP(io) {
        m := *p
        var g := m.Lock(io) else {
            return -2
        }
        g.Store(some(&n)) // <- E0605
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

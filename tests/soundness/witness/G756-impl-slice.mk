// verdict: reject E0602
// note: G756-impl-ref with `[]U`: K's impl of Src[Mutex[?&i32]].Items hands back a slice of a global [2]Mutex[?&i32]. E0602 at the impl's return. If accepted, a release build prints `before 5 after 1515870810` (dev shows no change). Acceptance means the hole reopened (mutant M-D756-impl)
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)
var GA: [2]Mutex[?&i32] = [Mutex[?&i32].New(none), Mutex[?&i32].New(none)]

interface Src[U] {
    Items(&self): []U
}

struct K {
    k: int
}

impl Src[Mutex[?&i32]] for K {
    Items(&self): []Mutex[?&i32] {
        return GA[..] // <- E0602
    }
}

Fwd[U, T: Src[U]](t: &T): []U {
    return t.Items()
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    kk := K(k: 1)
    s := Fwd[Mutex[?&i32], K](&kk)
    var g := s[0].Lock(io) else {
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
    var g := GA[0].Lock(io) else {
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

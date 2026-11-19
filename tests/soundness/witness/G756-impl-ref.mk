// verdict: reject E0602
// note: an interface method's `&U` in a generic body. Src[U].Get(&self): &U names self; K's impl names no source (K cannot supply a Mutex) and returns &GM, so Fwd[U, T: Src[U]](t: &T): &U returns t.Get() from t, and a caller that trusted it would judge a store through it against kk. An impl method whose interface method names a source has its boundary judged as a sourced one (exits.go implSourced): E0602 at the impl's return. If accepted, a dev build prints `before 5 after 1515870810`. Acceptance means the hole reopened (mutant M-D756-impl)
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

interface Src[U] {
    Get(&self): &U
}

struct K {
    k: int
}

impl Src[Mutex[?&i32]] for K {
    Get(&self): &Mutex[?&i32] {
        return &GM // <- E0602
    }
}

Fwd[U, T: Src[U]](t: &T): &U {
    return t.Get()
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    kk := K(k: 1)
    m := Fwd[Mutex[?&i32], K](&kk)
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

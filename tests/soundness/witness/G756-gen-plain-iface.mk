// verdict: reject E0602
// note: a type parameter with a Plain bound: G756-gen-iface as `Launder[T: Plain, S: Src[T]](s: &S, k: &i32): T from s, k`. Plain is a copy kind, not 'holds no loan': &T is Plain, so T: Plain admits &Mutex[?&i32]. E0602 at the return. An exemption of Plain type parameters (`!w.plain(t)` in firstTakes) would accept it, and the global would read a dead frame in dev and release. Mutant M-D756-gen-plain, which restores that exemption, accepts it. The impl method that returns the global names no source while its interface method names self, so its boundary is judged as a sourced one (implSourced): E0602 at the impl's return. An interface method that names a source, as every interface method does by R9 (Self mentions every type parameter), has its bare T judged at the impl, as its &U and []U are, so the generic caller does not report; the mutant row's implSourced cut is what flips this witness
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

interface Src[R] {
    Get(&self): R
}

struct K {
    k: int
}

impl Src[&Mutex[?&i32]] for K {
    Get(&self): &Mutex[?&i32] {
        return &GM // <- E0602
    }
}

Launder[T: Plain, S: Src[T]](s: &S, k: &i32): T from s, k {
    return s.Get()
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    k: i32 = 0
    kk := K(k: 1)
    m := Launder[&Mutex[?&i32], K](&kk, &k)
    var n: i32 = 5
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

// verdict: reject E0602
// note: G756-static-deep's GB returned by an impl under a sourced interface method, and forwarded by a generic Fwd under `from s`; the caller stores a local through b.m. The generic caller trusts the interface's source, so the impl's boundary must refuse &GB, and it does, since GB takes stores below its first level (staticOf): E0602 at the impl's return. If accepted, its run faults at the first Lock only because the evaluated Mutex is emitted read-only. Acceptance means the every-level rule for a named global is cut (mutant M-D756-static-deep)
struct Box {
    m: &Mutex[?&i32]
}

MakeBox(a: &Arena): Box from a {
    m := a.Put(Mutex[?&i32].New(none)) else {
        return MakeBox(a)
    }
    return Box(m: &*m)
}

var GB: Box = MakeBox(EvalArena())

interface Src[R] {
    Get(&self): R
}

struct K {
    k: int
}

impl Src[&Box] for K {
    Get(&self): &Box {
        return &GB // <- E0602
    }
}

Fwd[T, S: Src[T]](s: &S): T from s {
    return s.Get()
}

Stash(io: Io): int {
    kk := K(k: 1)
    var n: i32 = 5
    b := Fwd[&Box, K](&kk)
    var g := b.m.Lock(io) else {
        return -2
    }
    g.Store(some(&n))
    g.Unlock()
    return 0
}

Clobber(v: i32): i32 {
    var buf: [256]i32 = [v; 256]
    var s: i32 = 0
    for i in 0..256 {
        buf[i] = buf[i] +% Trunc[i32](i)
        s = s +% buf[i]
    }
    return s
}

Read(io: Io): i32 {
    var g := GB.m.Lock(io) else {
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
    st: fn(Io): int = Stash
    if st(sys.io) != 0 {
        return
    }
    before := Read(sys.io)
    cl: fn(i32): i32 = Clobber
    _ = cl(1000000)
    after := Read(sys.io)
    try sys.Print("before {before} after {after}\n", (before, after))
}

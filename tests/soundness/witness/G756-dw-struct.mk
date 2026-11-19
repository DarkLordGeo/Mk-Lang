// verdict: reject E0605
// note: a loan-free struct result W { m: &Mutex[?&i32] } points at the global GM, and a local is stored through it by a direct write: E0605 at the store. If accepted, `mk run` prints `before 32759 after 32759`, a read of Stash's dead frame. Mutant M-D756-result accepts it
// A loan-free struct result: Get(): W with W { m: &Mutex[?&i32] } pointing at the global
// GM; Stash stores &n through it by a direct write. Expect a reject (E0605); an accept printing a
// value other than 5 read Stash's dead frame.
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

struct W {
    m: &Mutex[?&i32]
}

Get(): W {
    return W(m: &GM)
}

Stash(io: Io): int {
    w := Get()
    var n: i32 = 5
    var g := w.m.Lock(io) else {
        return -2
    }
    p := g.Mut()
    *p = some(&n) // <- E0605
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

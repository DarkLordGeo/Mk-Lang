// verdict: reject E0605
// note: G756-b27a's shape with a direct write, `*g.Mut() = some(&n)`, through the guard of the source-less Get's &GM: E0605. If accepted, `mk run` prints `before 32759 after 32759`, a read of a dead frame. Mutant M-D756-result accepts it
// A direct write in G756-b27a's shape: Get(): &Mutex[?&i32] returns &GM; *g.Mut() = some(&n).
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

Get(): &Mutex[?&i32] {
    return &GM
}

Stash(io: Io): int {
    m := Get()
    var n: i32 = 5
    var g := m.Lock(io) else {
        return -2
    }
    *g.Mut() = some(&n) // <- E0605
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

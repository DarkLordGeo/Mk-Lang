// verdict: reject E0605
// note: the Mutex inline in a global struct, Get(): &HH returning &GH with HH { m: Mutex[?&i32] }, a local stored through &h.m's guard by a direct write. Accepted, it prints `before 32759 after 32759`, a dead frame. Acceptance means the hole reopened (mutant M-D756-result)
// dw-inline (the Mutex inline in a global struct): Get(): &HH returns &GH, HH { m: Mutex[?&i32] }.
struct HH {
    m: Mutex[?&i32]
    k: int
}

var GH: HH = HH(m: Mutex[?&i32].New(none), k: 0)

Get(): &HH {
    return &GH
}

Stash(io: Io): int {
    hh := Get()
    var n: i32 = 5
    var g := hh.m.Lock(io) else {
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
    var g := GH.m.Lock(io) else {
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

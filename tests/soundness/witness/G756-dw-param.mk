// verdict: reject E0605
// note: Get(s: &str) has a carrier parameter and no `from`, so by R9 its &Mutex[?&i32] result may point only into global memory; a local is stored through it by a direct write. Accepted, it prints `before 32758 after 32758`, a dead frame. Acceptance means the hole reopened (mutant M-D756-result)
// dw-param (through a carrier parameter with no `from`): Get(s: &str) returns &GM. Expect a reject.
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

Get(s: &str): &Mutex[?&i32] {
    return &GM
}

Stash(io: Io): int {
    t := "x"
    m := Get(&t)
    var n: i32 = 5
    var g := m.Lock(io) else {
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

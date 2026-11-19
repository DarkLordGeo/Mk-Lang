// verdict: reject E0605
// note: Get's result names no source, so by R9 it may point only into global memory, and the caller holds the static pseudo-loan of global memory for it, so the store of a local through it is E0605, as with m := &GM. Accepted, `mk run` reads a dead frame. Acceptance means the hole reopened (mutant M-D756-result)
// B27a, with no keep: a function returns a reference to a
// global Mutex[?&i32]; a local's reference is stored through it; the frame
// ends, the stack is clobbered, and the global is read directly. Expect a
// reject (E0605 or a loan error); an accept that prints a changed value is
// a base-checker hole.
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
    g.Store(some(&n)) // <- E0605
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

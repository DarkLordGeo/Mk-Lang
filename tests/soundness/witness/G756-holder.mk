// verdict: reject E0605
// note: the source-less Get's reference to the global GM is held in a struct, read back by `Holder.Get(&self) from self`, and a local is stored through its guard by a direct write. The static loan reaches m through the holder, and the direct write walks on from the guard to the global: E0605. Both halves are needed: with either cut (mutants M-D756-result, M-D790-walk) it is accepted, and a run reads a dead frame (`before 32758 after 32758`)
// The core shape, with no keep: a function returns a reference to a
// global Mutex[?&i32]; a local's reference is stored through it; the frame
// ends, the stack is clobbered, and the global is read directly. Expect a
// reject (E0605 or a loan error); an accept that prints a changed value is
// a checker hole.
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

struct Holder {
    r: &Mutex[?&i32]
}

Holder.Get(&self): &Mutex[?&i32] from self {
    return self.r
}

Get(): &Mutex[?&i32] {
    return &GM
}

Stash(io: Io): int {
    h := Holder(r: Get())
    m := h.Get()
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

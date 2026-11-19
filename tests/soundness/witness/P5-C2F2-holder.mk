// verdict: reject E0505
// note: Holder.Get(&self): &Mutex[?&i32] from self hands back the reference Holder holds;
// the result holds the Holder local's loan, one level above the Mutex, and a store of a mortal reference through its
// guard walks on to the Mutex: E0505. If it landed on the copy, a run would read a dead frame. Mutant M-D790-walk accepts it
// Checked user code, no unsafe, no std.core:
// Holder.Get(&self): &Mutex from self returns the reference Holder holds,
// so its result holds the loan of the Holder local (H(&h)), one level above
// the Mutex it points at (allowed: H(p) covers B(p)). Stash wraps the
// caller's Mutex in a local Holder, takes it back with Get, and stores a
// reference to its own local n by a direct write through the guard. The
// direct write rule walks guard -> h and stops (Holder is not a view), so
// the store lands on h. Stash returns, the stack is clobbered, and Run
// reads the Mutex before and after: result before*10000000+after; a sound
// run prints 777770077777 (n is the canary 77777); anything else is what
// the dead frame's slot holds after the stack was reused.
struct Holder {
    r: &Mutex[?&i32]
}

Holder.Get(&self): &Mutex[?&i32] from self {
    return self.r
}

Stash(io: Io, m: &Mutex[?&i32]): int {
    h := Holder(r: m)
    st := h.Get()
    var n: i32 = 77777
    var g := st.Lock(io) else {
        return -2
    }
    p := g.Mut()
    *p = some(&n) // <- E0505
    g.Unlock()
    return Cast[int](n) - 77777
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

Read(io: Io, m: &Mutex[?&i32]): i32 {
    var g := m.Lock(io) else {
        return -1
    }
    var v: i32 = -1
    if r := *g.Ref() {
        v = *r
    }
    g.Unlock()
    return v
}

Run(io: Io): int {
    m := Mutex[?&i32].New(none)
    if Stash(io, &m) != 0 {
        return -2
    }
    before := Read(io, &m)
    _ = Clobber(1000000)
    after := Read(io, &m)
    return Cast[int](before) * 10000000 + Cast[int](after)
}

export Main(sys: &System): WriteError!void {
    r := Run(sys.io)
    try sys.Print("result {r}\n", (r))
}

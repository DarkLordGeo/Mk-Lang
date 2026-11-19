// verdict: reject E0505
// note: through `from s` and a shared reference to a Mutex: the result holds the loan of the local sv,
// which holds `&Mutex[?&i32]`. A shared reference to an
// interior-mutable type keeps writing possible, so the guard write walks on to the Mutex: E0505. Mutant
// M-D790-walk, which stops the walk at sv, accepts it, and a run then reads a dead frame
// The checked form of the shape. Get(s: &S): &Mutex from s
// returns s.r, a reference S only holds; the clause gives the result H(s),
// a loan of the caller's local sv, one level above the Mutex it points at.
// Stash writes a reference to its own local n through the guard (a direct
// write); the walk stops at the plain local sv. After Stash returns and the
// stack is clobbered, Run reads m. Result: before*10^7 + after (before is read
// right after Stash, after a 4 KiB clobber); any value other than 5 is garbage.
struct S {
    r: &Mutex[?&i32]
}

Get(s: &S): &Mutex[?&i32] from s {
    return s.r
}

Stash(io: Io, m: &Mutex[?&i32]): int {
    var pad: [64]i32 = [7; 64]
    for i in 0..64 {
        pad[i] = pad[i] +% Trunc[i32](i)
    }
    if pad[3] == 0 {
        return -9
    }
    sv := S(r: m)
    st := Get(&sv)
    var n: i32 = 5
    var g := st.Lock(io) else {
        return -2
    }
    p := g.Mut()
    *p = some(&n) // <- E0505
    g.Unlock()
    return 0
}

Clobber(v: i32): i32 {
    var buf: [1024]i32 = [v; 1024]
    var s: i32 = 0
    for i in 0..1024 {
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
    var before: i32 = -1
    var g := m.Lock(io) else {
        return -3
    }
    if r := *g.Ref() {
        before = *r
    }
    g.Unlock()
    _ = Clobber(1000000)
    after := Read(io, &m)
    return Cast[int](before) * 10000000 + Cast[int](after)
}

export Main(sys: &System): WriteError!void {
    r := Run(sys.io)
    try sys.Print("result {r}\n", (r))
}

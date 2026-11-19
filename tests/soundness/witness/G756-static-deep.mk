// verdict: reject E0605
// note: a global GB of `Box { m: &Mutex[?&i32] }` takes no store at its first level, but its initializer put the Mutex in EvalArena, so its second level does. `b := &GB` in Stash's own body and a guard of b.m store a local into GB's Mutex. A named global takes stores, and counts as writable, when a reference in its bytes reaches memory that takes stores at any level (setup.go staticOf), so the store lands on GB's node: E0605. If accepted, its run faults at the first Lock (`invalid memory access` in Mutex[T].Lock, std/core/sync_mutex.mk) only because the evaluated Mutex is emitted read-only; were it writable, the same text would read a dead frame. Acceptance means the rule is cut (mutant M-D756-static-deep)
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

Get(): &Box {
    return &GB
}

Stash(io: Io): int {
    b := &GB
    var n: i32 = 5
    var g := b.m.Lock(io) else {
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

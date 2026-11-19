// verdict: reject E1002
// note: the const value rule keeps a const whose value reaches memory that takes stores: a slice const GS whose element points at an EvalArena Mutex, judged on the value, is global memory that takes stores, so a store of a frame's loan through it is E0605. Run, it would panic with an invalid memory access at std/core/sync_mutex.mk (the evaluated Mutex is emitted read-only), so no dead read is shown; the refusal keeps the rule from resting on that emission. The const itself is refused first: its pointee holds a Mutex by value, which a const's read-only data cannot carry, so it is E1002 at the const in k and borrow's constTakes is not reached; the G756-const-view witnesses keep the value rule witnessed (mutant M-D756-constvalue)
import k

Stash(io: Io): int {
    b := k.GS[0]
    var n: i32 = 5
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

export Main(sys: &System): WriteError!void {
    st: fn(Io): int = Stash
    if st(sys.io) != 0 {
        return
    }
    before := k.Read(sys.io)
    cl: fn(i32): i32 = Clobber
    _ = cl(1000000)
    after := k.Read(sys.io)
    try sys.Print("before {before} after {after}\n", (before, after))
}

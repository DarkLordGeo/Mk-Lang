// verdict: ok
// case: H33
// note: static half only. `Shared.New` and `Free` take `&Heap`; `Get` and `Set` are `Ref` and `Store`. No loan on an allocator is involved. `Shared.New` consumes the ring only on success, so the ring is bound first and its `onerr` releases it on that error edge (skipped once moved, R21); the cycle leaks. `Guard.Store` of a linear T is E0701, so the clone goes in with `Replace(g.Mut(), x)`, whose old value (none) is consumed
import std.sync

struct Ring {
    next: Mutex[?sync.Shared[Ring]]
}

Cycle(h: &Heap, io: Io): OutOfMemory|Canceled!void {
    ring := Ring(next: Mutex.New(none))
    onerr Take(ring)
    x := try sync.Shared[Ring].New(h, ring)
    onerr x.Free(h)
    y := x.Clone()
    onerr y.Free(h)
    r := x.Ref()
    var g := try r.next.Lock(io)
    old := Replace(g.Mut(), some(y))
    if s := old {
        s.Free(h)
    }
    g.Unlock()
    x.Free(h)
}

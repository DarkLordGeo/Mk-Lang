// mk test: "leaks" ends with live allocations, so the counting heap reports it and `mk test` exits 1.
// A HeapList cannot be dropped (it is linear, E0402), so the leak is a reference-counted cycle, as in hard case H33:
// the ring's list holds a clone of the Shared that owns the ring, so x.Free leaves the Shared and the list's
// buffer live. The clone goes in with Push, not Guard.Store: Store of a linear T is E0701, because it would drop
// the old value. A Shared is linear and not Free (its Free takes the Heap), so the list frees with
// FreeEmpty, which Release reaches only on the error edges before the push, where the list is empty. Shared.New consumes the ring only on success, so
// the ring is bound first and Release frees its empty list on that error edge (skipped once moved, R21).
import std.sync

struct Ring {
    next: Mutex[HeapList[sync.Shared[Ring]]]
}

Release(a: &Alloc, r: Ring) {
    Ring(next) := r
    list := next.Into()
    list.FreeEmpty(a)
}

test "leaks" (sys: &System) {
    h := sys.heap
    a := h.Alloc()
    ring := Ring(next: Mutex.New(HeapList[sync.Shared[Ring]].New(a)))
    onerr Release(a, ring)
    x := try sync.Shared[Ring].New(h, ring)
    onerr x.Free(h)
    y := x.Clone()
    onerr y.Free(h)
    r := x.Ref()
    var g := try r.next.Lock(sys.io)
    onerr g.Unlock()
    try g.Mut().Push(a, y)
    g.Unlock()
    x.Free(h)
}

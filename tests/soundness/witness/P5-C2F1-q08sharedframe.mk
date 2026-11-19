// verdict: reject E0505
// note: Make clones a sync.Shared[Mutex[?&i32]], stores a reference to its own local by a direct write through the clone's guard (*m = some(&n)), frees the clone and returns the original handle. A direct write through a view stores into the allocator the clone lives in, so the heap box the clone views holds n's loan: E0505 at the store. If the walk stopped at the clone, only the freed clone would hold n's loan, and a run would print a value other than 5 (a dead frame). Mutant M-D790-alloc accepts it
// The write direction through Shared[T].New, with a dead
// frame as the result. Make builds a Shared over a Mutex[?&i32], clones it, stores a
// reference to its own local n by a direct write through the clone's
// guard (`*m = some(&n)`), frees the clone and returns the original handle.
// If the direct write rule walked guard -> clone and stopped before the heap,
// only the clone, now freed, would hold n's loan: the returned handle would hold
// h's loan alone and pass the boundary check. Run clobbers the stack and
// reads the box: a value other than 5 is garbage from Make's dead frame.
import std.sync

Make(h: &Heap, io: Io): OutOfMemory!sync.Shared[Mutex[?&i32]] from h {
    s := try sync.Shared[Mutex[?&i32]].New(h, Mutex[?&i32].New(none))
    var n: i32 = 5
    c := s.Clone()
    var g := c.Ref().Lock(io) else {
        c.Free(h)
        return s
    }
    m := g.Mut()
    *m = some(&n) // <- E0505
    g.Unlock()
    c.Free(h)
    return s
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

Read(io: Io, s: &sync.Shared[Mutex[?&i32]]): i32 {
    var g := s.Ref().Lock(io) else {
        return -1
    }
    var v: i32 = -1
    if r := *g.Ref() {
        v = *r
    }
    g.Unlock()
    return v
}

Run(h: &Heap, io: Io): int {
    s := Make(h, io) else {
        return -1
    }
    _ = Clobber(1000000)
    v := Read(io, &s)
    s.Free(h)
    return Cast[int](v)
}

export Main(sys: &System): WriteError!void {
    r := Run(sys.heap, sys.io)
    try sys.Print("result {r}\n", (r))
}

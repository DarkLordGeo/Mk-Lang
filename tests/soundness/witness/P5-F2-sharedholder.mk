// verdict: reject E0505 E0603
// note: a guard write through a shared reference to a Shared[Mutex[?&i32]] held in a struct. The guard write stores n's loan into the heap the Shared lives in: E0505 at the store.
// A walk that stopped at views would leave the write on the holder. A shared
// reference to a view that is no allocator keeps writing possible (R11), so the walk goes on to the Shared box and
// the mortal reference escaping through the result is refused (E0603). Mutant
// M-D790-views accepts it
// The shape: the Shared is reached through a Holder that holds
// a `&Shared` field (a view). Make builds a Shared over a Mutex[?&i32], puts a reference
// to it in a Holder, and stores a reference to its own local n by a direct write through
// the Holder -> Shared -> guard chain, then returns the handle. The direct write rule's
// walk-on would not fire on the Holder root if it stopped at views, since its field type `&Shared[..]`
// reaches the Mutex's interior only through the Shared view. The store would land on the
// Holder local and be lost, and the returned handle's Mutex would
// hold n's loan. Run clobbers the stack and reads the box: a value other than 5 is garbage
// from Make's dead frame. The equivalent write with the Shared reached directly is E0505.
import std.sync

struct Holder {
    r: &sync.Shared[Mutex[?&i32]]
}

Holder.Get(&self): &sync.Shared[Mutex[?&i32]] from self {
    return self.r
}

Make(h: &Heap, io: Io): OutOfMemory!sync.Shared[Mutex[?&i32]] from h {
    s := try sync.Shared[Mutex[?&i32]].New(h, Mutex[?&i32].New(none))
    var n: i32 = 5
    hd := Holder(r: &s)
    g2 := hd.Get()
    var g := g2.Ref().Lock(io) else {
        return s
    }
    m := g.Mut()
    *m = some(&n) // <- E0505
    g.Unlock()
    return s // <- E0603
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

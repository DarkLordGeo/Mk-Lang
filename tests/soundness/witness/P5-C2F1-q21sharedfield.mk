// verdict: reject E0505
// note: P5-C2F1-q08sharedframe with the Shared in Pack's field s beside a str, for field-level loan sets: E0505 at the store through the clone's guard. If accepted, a run prints a value other than the canary 77777 (a dead frame). Mutant M-D790-alloc accepts it
// P5-C2F1-q08sharedframe with the handle in a struct field,
// for field-level loan sets. Pack holds the Shared in field s beside a
// str field. Make clones p.s, stores a reference to its own local n by a
// direct write through the clone's guard, frees the clone and returns the
// Pack. Run clobbers the stack and reads the box through p.s. A value other
// than the canary 77777 is what the dead frame's slot holds.
import std.sync

struct Pack {
    tag: str
    s: sync.Shared[Mutex[?&i32]]
}

Make(h: &Heap, io: Io): OutOfMemory!Pack from h {
    s := try sync.Shared[Mutex[?&i32]].New(h, Mutex[?&i32].New(none))
    var p := Pack(tag: "static", s: s)
    var n: i32 = 77777
    c := p.s.Clone()
    var g := c.Ref().Lock(io) else {
        c.Free(h)
        return p
    }
    m := g.Mut()
    *m = some(&n) // <- E0505
    g.Unlock()
    c.Free(h)
    if n != 77777 {
        p.tag = "changed"
    }
    return p
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
    p := Make(h, io) else {
        return -1
    }
    _ = Clobber(1000000)
    v := Read(io, &p.s)
    Pack(tag: _, s) := p
    s.Free(h)
    return Cast[int](v)
}

export Main(sys: &System): WriteError!void {
    r := Run(sys.heap, sys.io)
    try sys.Print("result {r}\n", (r))
}

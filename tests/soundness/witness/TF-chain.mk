// verdict: reject E0505
// note: a chain of task flows: Forward's `*a into b` moves what ch holds into mid, and the reader's `*ch into out` moves what mid holds into `*p`; a store into ch after both spawns reaches `*p` through both. Accepted, Main reads a dead frame through p
Forward(a: &Chan[&i32], io: Io, b: &Chan[&i32]) *a into b {
    v := a.Recv(io) else {
        return
    }
    b.Send(io, v) else {}
}

RecvInto(ch: &Chan[&i32], io: Io, out: mut &i32) *ch into out {
    v := ch.Recv(io) else {
        return
    }
    *out = v
}

SendOne(ch: &Chan[&i32], io: Io, v: &i32 into ch) {
    ch.Send(io, v) else {}
}

Go(sys: &System, p: mut &i32): OutOfMemory!void {
    short: i32 = 22
    ch := try Chan[&i32].New(sys.heap.Alloc(), 1)
    mid := try Chan[&i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        s.go RecvInto(&mid, sys.io, p)
        s.go Forward(&ch, sys.io, &mid)
        ch.Send(sys.io, &short) else {} // <- E0505
    }
    mid.Free(sys.heap.Alloc())
    ch.Free(sys.heap.Alloc())
}

Clobber(n: i32): i32 {
    var a: [64]i32 = [0; 64]
    for i in 0..64 {
        a[i] = n + 1
    }
    return a[n]
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    long: i32 = 1
    var p := &long
    try Go(sys, mut p)
    before := *p
    _ = Clobber(7)
    after := *p
    try sys.Print("before {before}, after {after}\n", (before, after))
}

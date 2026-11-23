// verdict: reject E0505
// note: the reader writes the received reference into `out.r` of a `mut Out`; the second task's store into ch reaches it. Accepted, Main reads a dead frame through o.r
struct Out {
    r: &i32
}

RecvInto(ch: &Chan[&i32], io: Io, out: mut Out) *ch into out {
    v := ch.Recv(io) else {
        return
    }
    out.r = v
}

SendOne(ch: &Chan[&i32], io: Io, v: &i32 into ch) {
    ch.Send(io, v) else {}
}

Go(sys: &System, o: mut Out): OutOfMemory!void {
    short: i32 = 22
    ch := try Chan[&i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        s.go RecvInto(&ch, sys.io, o)
        s.go SendOne(&ch, sys.io, &short) // <- E0505
    }
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
    var o := Out(r: &long)
    try Go(sys, mut o)
    before := *o.r
    _ = Clobber(7)
    after := *o.r
    try sys.Print("before {before}, after {after}\n", (before, after))
}

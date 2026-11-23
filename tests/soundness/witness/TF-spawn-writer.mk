// verdict: reject E0505
// note: a task's `*ch into out` holds until the scope joins: a second task's store of `&short` into ch reaches `*p`, which outlives short. Accepted, the reader moves the received reference into `*p` and Main reads a dead frame
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
    scope s := sys.io.Scope() {
        s.go RecvInto(&ch, sys.io, p)
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
    var p := &long
    try Go(sys, mut p)
    before := *p
    _ = Clobber(7)
    after := *p
    try sys.Print("p points at short: {before}, after another call: {after}\n", (before, after))
}

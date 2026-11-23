// verdict: reject E0505
// note: a forwarder's `*a into b` into a `&Chan` parameter: the second task's store into the local ch reaches the parameter's channel, which outlives short. Accepted, Main receives a reference into a dead frame
Forward(a: &Chan[&i32], io: Io, b: &Chan[&i32]) *a into b {
    v := a.Recv(io) else {
        return
    }
    b.Send(io, v) else {}
}

SendOne(ch: &Chan[&i32], io: Io, v: &i32 into ch) {
    ch.Send(io, v) else {}
}

Go(sys: &System, out: &Chan[&i32]): OutOfMemory!void {
    short: i32 = 22
    ch := try Chan[&i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        s.go Forward(&ch, sys.io, out)
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

export Main(sys: &System): OutOfMemory|WriteError|Canceled|Closed!void {
    out := try Chan[&i32].New(sys.heap.Alloc(), 1)
    try Go(sys, &out)
    p := try out.Recv(sys.io)
    before := *p
    _ = Clobber(7)
    after := *p
    try sys.Print("before {before}, after {after}\n", (before, after))
    out.Free(sys.heap.Alloc())
}

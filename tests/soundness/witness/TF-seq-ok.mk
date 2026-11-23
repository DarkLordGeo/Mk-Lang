// verdict: ok
// note: no scope: the reader runs to its end before the send, so its `*ch into out` is applied once, at the call, and the later store into ch does not reach `*p`. The task flow rule must not touch plain sequential calls
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
    ch.Close()
    RecvInto(&ch, sys.io, p)
    SendOne(&ch, sys.io, &short)
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

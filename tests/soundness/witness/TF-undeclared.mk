// verdict: reject E0604
// note: TF-outlives-ok without `v into p`: the task's `*ch into out` carries v, sent after the spawn, into `*p`, which the signature does not declare, so Go's caller would not know p may hold v. Accepted, a caller passing a short-lived v keeps p pointing at it
RecvInto(ch: &Chan[&i32], io: Io, out: mut &i32) *ch into out {
    v := ch.Recv(io) else {
        return
    }
    *out = v
}

SendOne(ch: &Chan[&i32], io: Io, v: &i32 into ch) {
    ch.Send(io, v) else {}
}

Go(sys: &System, p: mut &i32, v: &i32): OutOfMemory!void {
    ch := try Chan[&i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        s.go RecvInto(&ch, sys.io, p)
        ch.Send(sys.io, v) else {} // <- E0604
        s.go SendOne(&ch, sys.io, v)
    }
    ch.Free(sys.heap.Alloc())
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    long: i32 = 1
    other: i32 = 2
    var p := &long
    try Go(sys, mut p, &other)
    try sys.Print("got {n}\n", (n: *p))
}

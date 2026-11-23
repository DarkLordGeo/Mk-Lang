// verdict: ok
// note: the value sent after the reader's spawn is the caller's v, declared `v into p`, so the task flow `*ch into out` carries into `*p` only what may live there
RecvInto(ch: &Chan[&i32], io: Io, out: mut &i32) *ch into out {
    v := ch.Recv(io) else {
        return
    }
    *out = v
}

SendOne(ch: &Chan[&i32], io: Io, v: &i32 into ch) {
    ch.Send(io, v) else {}
}

Go(sys: &System, p: mut &i32, v: &i32 into p): OutOfMemory!void {
    ch := try Chan[&i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        s.go RecvInto(&ch, sys.io, p)
        ch.Send(sys.io, v) else {}
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

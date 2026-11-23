// verdict: ok
// note: the reader is spawned on one branch and the send is made on the other: the task's `*ch into out` holds only where the scope holds the task, so the store on the branch with no reader does not reach `*p`
RecvInto(ch: &Chan[&i32], io: Io, out: mut &i32) *ch into out {
    v := ch.Recv(io) else {
        return
    }
    *out = v
}

SendOne(ch: &Chan[&i32], io: Io, v: &i32 into ch) {
    ch.Send(io, v) else {}
}

Go(sys: &System, p: mut &i32, read: bool): OutOfMemory!void {
    short: i32 = 22
    ch := try Chan[&i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        if read {
            ch.Close()
            s.go RecvInto(&ch, sys.io, p)
        } else {
            ch.Send(sys.io, &short) else {}
            _ = ch.Recv(sys.io) else {}
        }
    }
    ch.Free(sys.heap.Alloc())
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    long: i32 = 1
    var p := &long
    try Go(sys, mut p, false)
    try sys.Print("got {n}\n", (n: *p))
}

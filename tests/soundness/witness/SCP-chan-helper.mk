// verdict: reject E0503 E0505
// note: a task that receives from a channel into p (`*ch into out`), then a helper that spawns a sender of `&short` on the same channel; the helper's store into the scope reaches p through what the scope holds, so short and the channel are E0505 into `*p`. Accepted, p points at the dead short after the call (a walk that stops at the scope accepts it)
RecvInto(ch: &Chan[&i32], io: Io, out: mut &i32) *ch into out {
    v := ch.Recv(io) else {
        return
    }
    *out = v
}

SendOne(ch: &Chan[&i32], io: Io, v: &i32 into ch) {
    ch.Send(io, v) else {}
}

SpawnSend(s: &Scope, ch: &Chan[&i32] into s, io: Io, v: &i32 into ch) {
    s.go SendOne(ch, io, v)
}

Go(sys: &System, p: mut &i32): OutOfMemory!void {
    short: i32 = 22
    ch := try Chan[&i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        s.go RecvInto(&ch, sys.io, p)
        SpawnSend(&s, &ch, sys.io, &short) // <- E0505
    }
    ch.Free(sys.heap.Alloc()) // <- E0503
}

export Main(sys: &System): OutOfMemory!void {
    long: i32 = 1
    var p := &long
    try Go(sys, mut p)
    Use(p)
}

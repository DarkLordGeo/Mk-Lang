// verdict: reject E0505
// note: the reader's `*ch into pc` stores what it receives on ch through what it receives on pc: the parent sends `&short` into ch, then p into pc, so what pc reaches grows to `*p` after the store into ch, and the clause applied again reaches it. Accepted, Main reads a dead frame through p
R(ch: &Chan[&i32], pc: &Chan[mut &i32], io: Io) *ch into pc {
    v := ch.Recv(io) else {
        return
    }
    r := pc.Recv(io) else {
        return
    }
    *r = v
}

Go(sys: &System, p: mut &i32): OutOfMemory!void {
    short: i32 = 22
    ch := try Chan[&i32].New(sys.heap.Alloc(), 1)
    pc := try Chan[mut &i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        s.go R(&ch, &pc, sys.io)
        ch.Send(sys.io, &short) else {}
        pc.Send(sys.io, p) else {} // <- E0505
    }
    pc.Free(sys.heap.Alloc())
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

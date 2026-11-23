// verdict: reject E0505
// rule: R17
// note: a task's `*q into p` holds until the join, so a store into the channel after the reader is spawned reaches `*p`
Receive(ch: &Chan[&i32], io: Io, out: mut &i32) *ch into out {
    v := ch.Recv(io) else {
        return
    }
    *out = v
}

Late(io: Io, h: &Heap, p: mut &i32): OutOfMemory!void {
    short: i32 = 22
    ch := try Chan[&i32].New(h.Alloc(), 1)
    scope s := io.Scope() {
        s.go Receive(&ch, io, p)
        ch.Send(io, &short) else {} // <- E0505
    }
    ch.Free(h.Alloc())
}

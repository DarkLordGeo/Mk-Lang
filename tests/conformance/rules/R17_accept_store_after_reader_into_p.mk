// verdict: ok
// rule: R17
// note: a task's `*q into p` holds until the join; the value stored after the reader is spawned is declared `into p`, so it may live there
Receive(ch: &Chan[&i32], io: Io, out: mut &i32) *ch into out {
    v := ch.Recv(io) else {
        return
    }
    *out = v
}

Late(io: Io, h: &Heap, p: mut &i32, v: &i32 into p): OutOfMemory!void {
    ch := try Chan[&i32].New(h.Alloc(), 1)
    scope s := io.Scope() {
        s.go Receive(&ch, io, p)
        ch.Send(io, v) else {}
    }
    ch.Free(h.Alloc())
}

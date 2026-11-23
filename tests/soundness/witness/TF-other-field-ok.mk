// verdict: ok
// note: the reader takes `&pr.a`; a send into the other field pr.b of the same split local does not reach what the task reads, so its `*ch into out` stores nothing new into `*p`
RecvInto(ch: &Chan[&i32], io: Io, out: mut &i32) *ch into out {
    v := ch.Recv(io) else {
        return
    }
    *out = v
}

struct Pair {
    a: Chan[&i32]
    b: Chan[&i32]
}

Pair.Free(self, al: &Alloc) {
    Pair(a, b) := self
    a.Free(al)
    b.Free(al)
}

Go(sys: &System, p: mut &i32): OutOfMemory!void {
    short: i32 = 22
    pr := Pair(a: try Chan[&i32].New(sys.heap.Alloc(), 1), b: try Chan[&i32].New(sys.heap.Alloc(), 1))
    scope s := sys.io.Scope() {
        s.go RecvInto(&pr.a, sys.io, p)
        pr.a.Close()
        pr.b.Send(sys.io, &short) else {}
    }
    pr.Free(sys.heap.Alloc())
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    long: i32 = 1
    var p := &long
    try Go(sys, mut p)
    try sys.Print("{n}\n", (n: *p))
}

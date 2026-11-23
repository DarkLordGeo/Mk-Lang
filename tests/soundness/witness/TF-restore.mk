// verdict: reject E0505
// note: a loan a flow stored into a root and the parent then stored there too is walked like any other: the parent's send of W2(x: out) into pc lets the reader's `*ch into pc` reach out's channel, so the earlier Pair(s: &short) reaches it. Accepted, Main reads a dead frame
struct Pair {
    x: &Chan[&i32]
    s: &i32
}

struct W2 {
    x: &Chan[&i32]
}

R(ch: &Chan[Pair], pc: &Chan[W2], io: Io) *ch into pc {
    pr := ch.Recv(io) else {
        return
    }
    w := pc.Recv(io) else {
        return
    }
    w.x.Send(io, pr.s) else {}
}

Go(sys: &System, out: &Chan[&i32] into out, lp: &i32 into out): OutOfMemory!void {
    short: i32 = 22
    ch := try Chan[Pair].New(sys.heap.Alloc(), 1)
    pc := try Chan[W2].New(sys.heap.Alloc(), 1)
    loc := try Chan[&i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        s.go R(&ch, &pc, sys.io)
        ch.Send(sys.io, Pair(x: &loc, s: &short)) else {}
        ch.Send(sys.io, Pair(x: out, s: lp)) else {} // <- E0505
        pc.Send(sys.io, W2(x: out)) else {}
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
    out := try Chan[&i32].New(sys.heap.Alloc(), 1)
    long: i32 = 5
    try Go(sys, &out, &long)
    v := out.Recv(sys.io) else {
        return
    }
    before := *v
    _ = Clobber(7)
    after := *v
    try sys.Print("before {before}, after {after}\n", (before, after))
}

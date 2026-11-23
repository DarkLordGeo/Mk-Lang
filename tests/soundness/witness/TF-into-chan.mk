// verdict: reject E0505
// note: a task's `x into p` holds until the join too: W sends `&short` into the channel it receives on cc, so the parent's later send of out into cc carries short into out's channel, which outlives it. Accepted, Main receives a reference into a dead frame
W(cc: &Chan[&Chan[&i32]], io: Io, v: &i32 into cc) {
    r := cc.Recv(io) else {
        return
    }
    r.Send(io, v) else {}
}

Go(sys: &System, out: &Chan[&i32]): OutOfMemory!void {
    short: i32 = 22
    cc := try Chan[&Chan[&i32]].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        s.go W(&cc, sys.io, &short)
        cc.Send(sys.io, out) else {} // <- E0505
    }
    cc.Free(sys.heap.Alloc())
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
    try Go(sys, &out)
    v := out.Recv(sys.io) else {
        out.Free(sys.heap.Alloc())
        return
    }
    before := *v
    _ = Clobber(7)
    after := *v
    try sys.Print("before {before}, after {after}\n", (before, after))
    out.Free(sys.heap.Alloc())
}

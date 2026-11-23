// verdict: reject E0505
// note: a task's `x into p` holds until the join too: W stores `&short` through the `mut &i32` it receives on cc, so the parent's later send of p into cc carries short into `*p`. Accepted, Main reads a dead frame through p
W(cc: &Chan[mut &i32], io: Io, v: &i32 into cc) {
    r := cc.Recv(io) else {
        return
    }
    *r = v
}

Go(sys: &System, p: mut &i32): OutOfMemory!void {
    short: i32 = 22
    cc := try Chan[mut &i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        s.go W(&cc, sys.io, &short)
        cc.Send(sys.io, p) else {} // <- E0505
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
    long: i32 = 1
    var p := &long
    try Go(sys, mut p)
    before := *p
    _ = Clobber(7)
    after := *p
    try sys.Print("before {before}, after {after}\n", (before, after))
}

// verdict: reject E0505
// note: a call whose argument reaches the scope through a struct (Pool(s: &s)) holds its callee's clauses until the join, as one passing &s does. Accepted, Main reads a dead frame through p
R(ch: &Chan[&i32], pc: &Chan[mut &i32], io: Io) *ch into pc {
    v := ch.Recv(io) else {
        return
    }
    r := pc.Recv(io) else {
        return
    }
    *r = v
}

struct Pool {
    s: &Scope
}

Start(pl: &Pool, ch: &Chan[&i32] into pl, pc: &Chan[mut &i32] into pl, io: Io) *ch into pc {
    pl.s.go R(ch, pc, io)
}

Go(sys: &System, p: mut &i32): OutOfMemory!void {
    short: i32 = 22
    ch := try Chan[&i32].New(sys.heap.Alloc(), 1)
    pc := try Chan[mut &i32].New(sys.heap.Alloc(), 1)
    scope s := sys.io.Scope() {
        pl := Pool(s: &s)
        Start(&pl, &ch, &pc, sys.io)
        ch.Send(sys.io, &short) else {}
        pc.Send(sys.io, p) else {} // <- E0505
    }
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

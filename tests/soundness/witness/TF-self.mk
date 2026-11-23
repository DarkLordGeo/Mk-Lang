// verdict: reject E0505
// note: a self clause `*j into j` holds until the join too: the task moves what it receives on j.a through what it receives on j.b, so after the parent sends `&short` into jj.a and p into jj.b, short reaches `*p`. Accepted, Main reads a dead frame through p
struct J {
    a: Chan[&i32]
    b: Chan[mut &i32]
}

T(j: &J, io: Io) *j into j {
    v := j.a.Recv(io) else {
        return
    }
    r := j.b.Recv(io) else {
        return
    }
    *r = v
}

Go(sys: &System, p: mut &i32): OutOfMemory!void {
    short: i32 = 22
    jj := J(a: try Chan[&i32].New(sys.heap.Alloc(), 1), b: try Chan[mut &i32].New(sys.heap.Alloc(), 1))
    scope s := sys.io.Scope() {
        s.go T(&jj, sys.io)
        jj.a.Send(sys.io, &short) else {}
        jj.b.Send(sys.io, p) else {} // <- E0505
    }
    J(a, b) := jj
    b.Free(sys.heap.Alloc())
    a.Free(sys.heap.Alloc())
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

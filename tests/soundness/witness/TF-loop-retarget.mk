// verdict: reject E0505
// note: a spawn in a loop whose p argument changes (dst = &big) grows the flow's p loans; the flow's walk must start again from them, so the later sends of r into ch reach big, which outlives short. Accepted, Main reads short after its block has ended
Forward(a: &Chan[&i32], io: Io, b: &Chan[&i32]) *a into b {
    v := a.Recv(io) else {
        return
    }
    b.Send(io, v) else {}
}

Clobber(n: i32): i32 {
    var a: [64]i32 = [0; 64]
    for i in 0..64 {
        a[i] = n + 1
    }
    return a[n]
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    k: i32 = 1
    big := try Chan[&i32].New(sys.heap.Alloc(), 8)
    c1 := try Chan[&i32].New(sys.heap.Alloc(), 8)
    ch := try Chan[&i32].New(sys.heap.Alloc(), 8)
    ch0 := try Chan[&i32].New(sys.heap.Alloc(), 8)
    ch.Send(sys.io, &k) else {}
    var dst := &c1
    if k > 0 {
        short: i32 = 33
        r := &short
        ch0.Send(sys.io, r) else {}
        scope s := sys.io.Scope() {
            s.go Forward(&ch0, sys.io, &c1)
            for _ in 0..3 {
                s.go Forward(&ch, sys.io, dst)
                dst = &big
            }
            ch.Send(sys.io, r) else {} // <- E0505
            ch.Send(sys.io, r) else {}
        }
    }
    x := big.Recv(sys.io) else {
        return
    }
    y := big.Recv(sys.io) else {
        return
    }
    _ = Clobber(7)
    a := *x
    b := *y
    try sys.Print("got {a} {b}\n", (a, b))
}

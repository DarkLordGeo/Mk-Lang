// verdict: reject E0505
// note: locals only: the reader's `*ch into out` carries the second task's `&short` into q, a local of the enclosing block, so short's block end is E0505 (reported at the loan) while q is read after it. Accepted, q refers to short after short's block has ended
RecvInto(ch: &Chan[&i32], io: Io, out: mut &i32) *ch into out {
    v := ch.Recv(io) else {
        return
    }
    *out = v
}

SendOne(ch: &Chan[&i32], io: Io, v: &i32 into ch) {
    ch.Send(io, v) else {}
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
    var q := &long
    if long > 0 {
        short: i32 = 33
        ch := try Chan[&i32].New(sys.heap.Alloc(), 1)
        scope s := sys.io.Scope() {
            s.go RecvInto(&ch, sys.io, mut q)
            s.go SendOne(&ch, sys.io, &short) // <- E0505
        }
        ch.Free(sys.heap.Alloc())
    }
    before := *q
    _ = Clobber(7)
    after := *q
    try sys.Print("before {before}, after {after}\n", (before, after))
}

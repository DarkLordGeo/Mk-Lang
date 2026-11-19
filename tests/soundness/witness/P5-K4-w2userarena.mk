// verdict: reject E0503
// note: the callee half in checked user code: Build(a: &Arena): List[Blk] from a pushes a view of a.Dup's
// copy into a List in a, and the callee drops the store. B keeps the loan of sa: E0503 at sa.Free. If accepted, a run prints result 47
// (freed memory). Mutant M-D791-keep accepts it
// The mechanism of P5-K4-w1pemder in checked user code, no std
// parser and no unsafe. Build(a: &Arena): List[Blk] from a copies a string
// into a and pushes a Blk viewing that copy into a List in a. Inside the
// callee, the view holds a's loan and Push stores it into the node behind a
// (`x into self`), but the signature carries no `a into a`, so the caller's
// sa never holds L(sa): a Blk read out of l.Done() holds held(sa), which is
// empty. sa is freed, refilled with 'X', and the view read.
struct Blk {
    b: []u8
}

Build(a: &Arena): OutOfMemory!List[Blk] from a {
    var l := List[Blk].New(a)
    s := try a.Dup("hello arena, this text lives in the first arena")
    try l.Push(Blk(b: s.AsBytes()))
    return l
}

Run(sys: &System): int {
    h := sys.heap
    var sa := Arena.New(h) else {
        return -1
    }
    l := Build(&sa) else {
        sa.Free()
        return -2
    }
    bs := l.Done()
    e := bs[0].b
    sa.Free() // <- E0503
    return Refill(h, e)
}

// ---- helpers shared by the arena witnesses ----

// Clobber overwrites the stack below its caller's frame.
Clobber(v: i32): i32 {
    var buf: [256]i32 = [v; 256]
    var s: i32 = 0
    for i in 0..256 {
        buf[i] = buf[i] +% Trunc[i32](i)
        s = s +% buf[i]
    }
    return s
}

// CountX counts the bytes of b that are 'X' (88).
CountX(b: []u8): int {
    var n := 0
    for i in 0..b.Len() {
        if b[i] == 88 {
            n += 1
        }
    }
    return n
}

// Refill takes a second arena from h and fills 32 blocks of 64 bytes of it with 'X',
// so memory that a freed arena gave back to h is overwritten, and returns
// CountX(e), read while the second arena lives. A program that reads freed
// memory through e answers with the number of 'X' bytes it saw.
Refill(h: &Heap, e: []u8): int {
    var two := Arena.New(h) else {
        return -40
    }
    for _ in 0..32 {
        fill := two.Slice[u8](64) else {
            two.Free()
            return -50
        }
        for i in 0..fill.Len() {
            fill[i] = 88
        }
    }
    n := CountX(e)
    two.Free()
    return n
}

export Main(sys: &System): WriteError!void {
    r := Run(sys)
    try sys.Print("result {r}\n", (r))
}

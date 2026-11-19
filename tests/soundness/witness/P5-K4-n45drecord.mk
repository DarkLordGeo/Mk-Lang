// verdict: reject E0503
// note: as P5-K4-n45ctxarena with a record parameter (a: &Arena, k: int): E0503 at sa.Free. If accepted, a run prints result 47
// (freed memory). Mutant M-D791-keep accepts it
// The callee half with the arena in a record parameter: Build(c: (a: &Arena, k: int)). Expect a reject.
struct Blk {
    b: []u8
}

Build(c: (a: &Arena, k: int)): OutOfMemory!List[Blk] from c {
    var l := List[Blk].New(c.a)
    s := try c.a.Dup("hello arena, this text lives in the first arena")
    try l.Push(Blk(b: s.AsBytes()))
    return l
}

Run(sys: &System): int {
    h := sys.heap
    var sa := Arena.New(h) else {
        return -1
    }
    l := Build((a: &sa, k: 1)) else {
        sa.Free()
        return -2
    }
    bs := l.Done()
    e := bs[0].b
    sa.Free() // <- E0503
    return Refill(h, e)
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

// Refill takes a second arena from h, fills 32 blocks of 64 bytes with 'X', and returns
// CountX(e) while it lives: a program reading freed memory through e answers with the X count.
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

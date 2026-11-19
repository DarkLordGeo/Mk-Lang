// verdict: reject E0503
// note: as P5-K4-q2midreset with the arena taken by value and freed: E0503 at a.Free. If accepted, a run prints
// result 47 (freed memory). Mutant M-D791-keep accepts it
// As P5-K4-q2midreset with the arena taken by value, TakeFree(h, a: Arena), which frees it
// and refills a second arena before reading the element. Expect E0503 or E0505 at a.Free; a run printing
// "result 47" reads freed, refilled memory.
struct Blk {
    b: []u8
}

Build(a: &Arena): OutOfMemory!List[Blk] from a {
    var l := List[Blk].New(a)
    s := try a.Dup("hello arena, this text lives in the first arena")
    try l.Push(Blk(b: s.AsBytes()))
    return l
}

TakeFree(h: &Heap, a: Arena): int {
    l := Build(&a) else {
        a.Free()
        return -2
    }
    bs := l.Done()
    e := bs[0].b
    a.Free() // <- E0503
    var two := Arena.New(h) else {
        return -4
    }
    if Fill(&two) != 0 {
        two.Free()
        return -3
    }
    n := CountX(e)
    two.Free()
    return n
}

Run(sys: &System): int {
    sa := Arena.New(sys.heap) else {
        return -1
    }
    return TakeFree(sys.heap, sa)
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

// Fill takes 32 blocks of 64 bytes from a and fills them with 'X'.
Fill(a: &Arena): int {
    for _ in 0..32 {
        fill := a.Slice[u8](64) else {
            return -50
        }
        for i in 0..fill.Len() {
            fill[i] = 88
        }
    }
    return 0
}

export Main(sys: &System): WriteError!void {
    r := Run(sys)
    try sys.Print("result {r}\n", (r))
}

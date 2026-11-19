// verdict: reject E0503
// note: the arena reached through a by-value Ctx { a: &Arena, tag: int }; the self filter
// drops the store at the parameter's allocator node n1A, no memNode, so B's keep holds sa's loan: E0503 at sa.Free. If accepted, a run prints result
// 47 (freed memory). Mutant M-D791-keep accepts it
// The callee half, with the arena behind a struct parameter: Build(c: Ctx) with Ctx { a: &Arena }
// copies a string into c.a and pushes a view of it into a List in c.a. The relief for a store into an arena parameter's own memory is for an
// `a: &Arena` parameter only, so this store is judged in full. Expect a reject (E0604 in Build or
// E0503 at sa.Free).
struct Blk {
    b: []u8
}

struct Ctx {
    a: &Arena
    tag: int
}

Build(c: Ctx): OutOfMemory!List[Blk] from c {
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
    l := Build(Ctx(a: &sa, tag: 1)) else {
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

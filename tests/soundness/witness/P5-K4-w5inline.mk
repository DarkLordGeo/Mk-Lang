// verdict: reject E0503
// note: a string Dup'd from sa is pushed into a
// List in sa; the self filter drops the string's loan of sa at the store, so the element read out of Done() would hold
// none. B keeps the loan of sa it steps through, so the
// element read back borrows sa: E0503 at sa.Free. If accepted, a run prints result 47. Mutant M-D791-keep accepts it
// P5-K4-w2userarena's Dup and Push, done in the
// caller's own body: Push (`x into self`) stores the copy's loan L(sa)
// into sa, so a Blk read out of l.Done() holds L(sa). Expected: E0503 at
// sa.Free.
struct Blk {
    b: []u8
}

Run(sys: &System): int {
    h := sys.heap
    var sa := Arena.New(h) else {
        return -1
    }
    var l := List[Blk].New(&sa)
    s := sa.Dup("hello arena, this text lives in the first arena") else {
        sa.Free()
        return -2
    }
    l.Push(Blk(b: s.AsBytes())) else {
        sa.Free()
        return -3
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

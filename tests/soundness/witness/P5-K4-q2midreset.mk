// verdict: reject E0501
// note: the read back in a callee whose arena is its mut Arena parameter, reset under the value (a
// loan of *a, whose place is the arena). A keep only at local arenas misses it: E0501 at a.Reset. If accepted, a run prints
// result 47 (freed memory). Mutant M-D791-keep accepts it
// The callee half one call further in. Mid(a: mut Arena) calls Build(&*a),
// whose self store the callee drops, reads the element, resets a, refills it and reads the element again. The
// arena is Mid's parameter, not a local, so a keep only at a local arena misses it. Expect E0501 or E0503 at
// a.Reset; a run printing "result 47" reads freed, refilled memory.
struct Blk {
    b: []u8
}

Build(a: &Arena): OutOfMemory!List[Blk] from a {
    var l := List[Blk].New(a)
    s := try a.Dup("hello arena, this text lives in the first arena")
    try l.Push(Blk(b: s.AsBytes()))
    return l
}

Mid(a: mut Arena): int {
    l := Build(&*a) else {
        return -2
    }
    bs := l.Done()
    e := bs[0].b
    a.Reset() // <- E0501
    if Fill(&*a) != 0 {
        return -3
    }
    return CountX(e)
}

Run(sys: &System): int {
    var sa := Arena.New(sys.heap) else {
        return -1
    }
    r := Mid(mut sa)
    sa.Free()
    return r
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

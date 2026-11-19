// verdict: reject E0503
// note: a direct element write l[0] = b.Ref()[..] stores a []u8 view of a Box's bytes into a List in arena la. B keeps only allocator loans, so if the store stayed with the list and never reached la, the element read back through Done would hold no loan of b, and b could be taken under it. A direct write through a view stores into the allocator the view lives in, with the self filter (a loan is never stored into its own target): la holds b's loan, E0503 at the Take. If accepted, a run prints result 48 (the freed box's slot refilled with 'X'). Mutant M-D790-alloc accepts it
// A non-allocator heap owner and a view element.
// A direct element write stores a []u8 view of a Box's bytes into an
// arena-backed List[[]u8]. Reading e := d[0] gives e = B(Done), which keeps
// only allocator loans (behind, transfer.go); the Box root is not an
// allocator, so e loses the Box loan. Box.Take frees the box under e; a
// second same-size box filled with 'X' (88) reuses the freed heap slot.
// CountX(e) > 0 is a dead read of the freed box; the checker rejects at
// the Take (E0503), as it does for an arena view (P5-K1-p01idxarena).
Run(h: &Heap): int {
    var la := Arena.New(h) else {
        return -1
    }
    var b := Box[[48]u8].New(h.Alloc(), [65; 48]) else {
        la.Free()
        return -2
    }
    var l := List[[]u8].New(&la)
    l.Push("".AsBytes()) else {
        _ = b.Take(h.Alloc())
        la.Free()
        return -3
    }
    l[0] = b.Ref()[..]
    d := l.Done()
    e := d[0]
    _ = b.Take(h.Alloc()) // <- E0503
    var b2 := Box[[48]u8].New(h.Alloc(), [88; 48]) else {
        la.Free()
        return -4
    }
    n := CountX(e)
    _ = b2.Take(h.Alloc())
    la.Free()
    return n
}

export Main(sys: &System): WriteError!void {
    r := Run(sys.heap)
    try sys.Print("result {r}\n", (r))
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
// so memory a freed arena gave back to h is overwritten, and returns CountX(e).
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

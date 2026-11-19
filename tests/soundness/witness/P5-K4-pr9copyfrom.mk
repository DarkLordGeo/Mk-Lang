// verdict: reject E0503 E0505
// note: through std's Slice.CopyFrom (`*src into self`), reachable from std as P5-K4-n41jsonlit and P5-K4-n43urlquery are. A string of sa is pushed into a List in sa (the self filter drops its loan of sa), Done's slice is copied into a frame array, sa is freed and out[0] read: E0505 at the push and the copy, E0503 at the free. If accepted, a run prints result 47 (a dead read). Mutant M-D791-keep accepts it
// A hole check through std's Slice.CopyFrom (`*src into self`):
// a string Dup'd from sa is pushed into a List in sa (the self filter drops its loan of sa), the list's Done
// slice is copied into a frame array, sa is freed and the heap refilled with 'X'; the X bytes out[0] reads are
// counted. Above 0: a dead read.
Run(h: &Heap): int {
    var sa := Arena.New(h) else {
        return -1
    }
    s := sa.Dup("hello arena, this text lives in the first arena") else {
        sa.Free()
        return -2
    }
    var l := List[str].New(&sa)
    l.Push(s) else { // <- E0505
        sa.Free()
        return -3
    }
    d := l.Done()
    var out: [1]str = [""]
    (mut out[..]).CopyFrom(d[..]) // <- E0505
    sa.Free() // <- E0503
    return Refill(h, out[0].AsBytes())
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

// Refill takes a second arena from h, fills 32 blocks of 64 bytes with 'X' and counts the X bytes e reads.
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
    r := Run(sys.heap)
    try sys.Print("result {r}\n", (r))
}

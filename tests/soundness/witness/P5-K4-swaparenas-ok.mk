// verdict: ok
// note: the narrowing of B's keep: double buffering, std's Swap (`*a into b, *b into a`) of two local arenas, both freed, nothing read back. Sound (result 3). With B's allocator keep at the call rule's `*q into p` step it would be refused (E0505 at the Swap, E0503 at the Free), whatever the order. The step reads `*q` of a reference to an allocator without the keep, since `*q` is the allocator's handle, not its memory. A rejection means the narrowing is gone (mutant M-D791-narrow)
// Precision. Double buffering: two local arenas swapped with std's
// Swap (`*a into b, *b into a`), then both freed. No value of either is read back. Sound (result 3).
Run(h: &Heap): int {
    var cur := Arena.New(h) else {
        return -1
    }
    var prev := Arena.New(h) else {
        cur.Free()
        return -1
    }
    s := cur.Dup("abc") else {
        cur.Free()
        prev.Free()
        return -2
    }
    n := s.Len()
    Swap(mut cur, mut prev)
    cur.Free()
    prev.Free()
    return n
}

export Main(sys: &System): WriteError!void {
    r := Run(sys.heap)
    try sys.Print("result {r}\n", (r))
}

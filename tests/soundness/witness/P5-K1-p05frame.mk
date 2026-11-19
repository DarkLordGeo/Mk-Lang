// verdict: reject E0505 E0603
// note: the frame form of P5-K1-p01idxarena's hole: Stash stores a reference to its own local n into a List in the caller's arena by an index write, ends the list by Done and returns element 0 `from a`. If the store stayed with the list, the result would carry no loan of n and pass the boundary check. The direct write stores n's loan into the arena: E0505 at the store, E0603 at the return. If accepted, a run prints a value other than 5, the live value (a dead frame). Mutant M-D790-alloc accepts it
// A dead frame through List.Done. Stash makes a list in the
// caller's arena, stores a reference to its own local n by an index write
// (the store stays with the local list l, a view, and does not reach the
// parameter's arena), ends the list by Done and returns element 0 `from a`.
// B(Done's result) is what a's node holds, so the element carries no loan of
// n and the boundary check passes. The frame ends, the stack is clobbered,
// and the caller reads through the element. 5 is the live value; any other
// value is the dead frame.
Stash(a: &Arena): ?&i32 from a {
    var l := List[?&i32].New(a)
    l.Push(none) else {
        return none
    }
    var n: i32 = 5
    l[0] = some(&n) // <- E0505
    d := l.Done()
    return d[0] // <- E0603
}

Clobber(v: i32): i32 {
    var buf: [256]i32 = [v; 256]
    var s: i32 = 0
    for i in 0..256 {
        buf[i] = buf[i] +% Trunc[i32](i)
        s = s +% buf[i]
    }
    return s
}

Run(h: &Heap): int {
    var la := Arena.New(h) else {
        return -1
    }
    r := Stash(&la)
    _ = Clobber(1000000)
    var v := -1
    if p := r {
        v = Cast[int](*p)
    }
    la.Free()
    return v
}

export Main(sys: &System): WriteError!void {
    r := Run(sys.heap)
    try sys.Print("result {r}\n", (r))
}

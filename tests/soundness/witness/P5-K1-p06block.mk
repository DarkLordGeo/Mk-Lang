// verdict: reject E0505
// note: the block form of P5-K1-p01idxarena's hole: one frame, an inner block's local n stored into a List in an outer arena by an index write and copied out through Done to e, declared outside the block. The direct write stores n's loan into the arena, which outlives the block: E0505 where e takes the element. If e held no loan of n, reading it after the block would be accepted (the checker verdict is the point; whether LLVM reuses n's slot is its own matter). Mutant M-D790-alloc accepts it
// One frame, no callee between the store and the read. An inner
// block declares n and a list in an outer arena, stores &n by an index write,
// ends the list by Done and copies element 0 out to e, declared outside the
// block. n ends with the block; l is moved and d is dead by then, so no live
// holder has n's loan, and e (B(d) = held(la)) has none. A second block
// fills a local array (its slots may reuse n's), then e is read. 5 is the
// live value; any other value is the dead block's slot. The checker verdict
// is the point here; whether the slot is reused is up to LLVM.
Run(h: &Heap, on: bool): int {
    var la := Arena.New(h) else {
        return -1
    }
    var e: ?&i32 = none
    if on {
        var n: i32 = 5
        var l := List[?&i32].New(&la)
        l.Push(none) else {
            la.Free()
            return -2
        }
        l[0] = some(&n)
        d := l.Done()
        e = d[0] // <- E0505
    }
    var sum: i32 = 0
    if on {
        var q: [16]i32 = [99; 16]
        for i in 0..16 {
            q[i] = q[i] +% Trunc[i32](i)
            sum = sum +% q[i]
        }
    }
    var v := -1
    if p := e {
        v = Cast[int](*p)
    }
    la.Free()
    return v * 10000 + Cast[int](sum) % 10000
}

export Main(sys: &System): WriteError!void {
    r := Run(sys.heap, true)
    try sys.Print("result {r}\n", (r))
}

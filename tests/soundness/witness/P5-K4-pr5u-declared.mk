// verdict: reject E0503 E0505
// note: a soundness check of the narrowing of B's keep: P5-K4-pr5u with the clause its callee asks for, `a: &Arena into xs`, beside `*a into xs`. The call stores sa's loan into arr, so the caller's sa.Free() is refused (E0503) with or without the keep; the narrowing (no keep when q refers to an allocator) does not reopen P5-K4-pr5u. The clause is enough on its own, so this witness guards the narrowing, is no hole, and has no mutant row
// The callee half through `*a into xs` instead of
// `from a`: Leak copies a string into a, pushes it into a List in a (a store the callee drops), reads it back and
// writes it into the caller's xs, declared `*a into xs`. The caller frees sa, refills the heap with 'X' from a
// second arena and counts the X bytes arr[0] reads. Above 0: a dead read.
Leak(xs: mut []str, a: &Arena into xs): int *a into xs {
    var l := List[str].New(a)
    s := a.Dup("hello arena, this text lives in the first arena") else {
        return -2
    }
    l.Push(s) else {
        return -3
    }
    d := l.Done()
    xs[0] = d[0]
    return 0
}

Run(sys: &System): int {
    h := sys.heap
    var sa := Arena.New(h) else {
        return -1
    }
    var arr: [1]str = [""]
    r := Leak(mut arr[..], &sa) // <- E0505
    sa.Free() // <- E0503
    if r != 0 {
        return r
    }
    return Refill(h, arr[0].AsBytes())
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
    r := Run(sys)
    try sys.Print("result {r}\n", (r))
}

// verdict: reject E0505 E0603
// note: Leak writes a reference to its own local into a List in the caller's arena by index, ends the list by Done and returns element 0 `from a`. The direct write stores the local's loan into the arena: E0505 at the store, E0603 at the return. If accepted, a run prints result 5253 (before 5, after 253: a dead frame). Mutant M-D790-alloc accepts it
// A dead frame through List.Done. A function writes a reference to its
// own local into a List in the caller's arena by index, ends the list by
// Done, and returns element 0 `from a`; the frame is gone, the stack is
// clobbered, and the reference is read.
Leak(a: &Arena): ?&i32 from a {
    var n: i32 = 5
    var l := List[?&i32].New(a)
    l.Push(none) else {
        return none
    }
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
    var a := Arena.New(h) else {
        return -1
    }
    r := Leak(&a) else {
        a.Free()
        return -2
    }
    before := *r
    _ = Clobber(1000000)
    after := *r
    a.Free()
    return Cast[int](before) * 1000 + Cast[int](after % 1000)
}

export Main(sys: &System): WriteError!void {
    r := Run(sys.heap)
    try sys.Print("result {r}\n", (r))
}

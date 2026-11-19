// verdict: reject E0503 E0604
// note: List.Done must not lose a loan level after a direct element store: index writes in a checked
// callee store a string of sa into a List in another arena; the direct write rule keeps the store on the list,
// so Done's result holds sa's loan one level above the element; B keeps the loan of sa it steps through (E0503 at sa.Free).
// The index writes also store p's loans into a, the arena the list lives in, which Two's signature does not declare:
// E0604 at both, asking for `p: Pair into a`. Only mutant M-D790-alloc-keep, which cuts both rules, accepts it, and a run
// then prints result 47 (freed memory)
// The hole through a clean checked callee boundary. Two builds a
// list in the caller's arena and fills it by index writes from a by-value
// Pair (R10 row 1). If the stores stayed on the local list, no `p into a` would be
// needed, and Done's slice would be returned `from a, p`. The caller's result
// holds H(la) and H(pair) = {L(la), L(sa)} at its own level; a read through
// it gives B(result) = held(la) and held(sa), and neither has L(sa): the
// strs point into sa, but only the slice holds sa's loan. The slice is
// dropped, sa is freed, its memory refilled, and the element read. Above 0:
// a dead read.
struct Pair {
    a: str
    b: str
}

Two(a: &Arena, p: Pair): OutOfMemory!mut []str from a, p {
    var l := List[str].New(a)
    try l.Push("")
    try l.Push("")
    l[0] = p.a // <- E0604
    l[1] = p.b // <- E0604
    return l.Done()
}

Run(h: &Heap): int {
    var la := Arena.New(h) else {
        return -1
    }
    var sa := Arena.New(h) else {
        la.Free()
        return -1
    }
    s := sa.Dup("hello arena, this text lives in the first arena") else {
        sa.Free()
        la.Free()
        return -2
    }
    d := Two(&la, Pair(a: s, b: s)) else {
        sa.Free()
        la.Free()
        return -3
    }
    e := d[0]
    sa.Free() // <- E0503
    var two := Arena.New(h) else {
        la.Free()
        return -4
    }
    t := two.Dup("XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX") else {
        two.Free()
        la.Free()
        return -5
    }
    var same := 0
    for i in 0..e.Len() {
        if e.AsBytes()[i] == 88 {
            same += 1
        }
    }
    _ = t
    two.Free()
    la.Free()
    return same
}

export Main(sys: &System): WriteError!void {
    r := Run(sys.heap)
    try sys.Print("result {r}\n", (r))
}

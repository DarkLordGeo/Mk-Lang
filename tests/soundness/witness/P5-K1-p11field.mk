// verdict: reject E0503
// note: List.Done must not lose a loan level after a direct element store: an index write into a
// List held in a local struct stores a string of sa into a List in another arena; the direct write rule keeps the
// store on the list, so Done's result holds sa's loan one level above the element. B keeps the loan of sa it steps
// through, so the element read back borrows sa: E0503 at sa.Free. The direct write also stores into the arena the list
// lives in, which closes the frame and block forms (P5-K1-p05frame, P5-K1-p06block). If accepted, a run prints result 47. Mutant
// M-D790-alloc-keep, which cuts both rules, accepts it
// P5-K1-p01idxarena with the list inside a local struct beside another field,
// for field-level loan sets: the index write lands on w.l, the list
// is taken out by a destructure and ended by Done, and element 0 is read.
// Above 0: a dead read.
struct W {
    l: List[str]
    k: int
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
    var w := W(l: List[str].New(&la), k: 1)
    w.l.Push("") else {
        sa.Free()
        la.Free()
        return -3
    }
    w.l[0] = s
    W(l, ..) := w
    d := l.Done()
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

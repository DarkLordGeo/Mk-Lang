// verdict: reject E0503
// note: List.Done must not lose a loan level after a direct element store: an index write l[0] = s
// stores a string of sa into a List in another arena; the direct write rule keeps the store on the list, so
// Done's result holds sa's loan one level above the element. B keeps the loan of sa it steps through, so the element
// read back borrows sa: E0503 at sa.Free. The direct write also stores into the arena the list lives in, which closes the frame and
// block forms (P5-K1-p05frame, P5-K1-p06block). If accepted, a run prints result 47 (freed memory). Mutant M-D790-alloc-keep, which cuts both rules, accepts it
// List.Done(self): mut []T, by value, automatic H(self). The
// element is stored by an index write on the list (l[0] = s), which the
// direct write rule keeps on the list variable and does not walk on to the
// arena. Done's result holds H(l); a read through it gives B(result), which
// reaches the arena's held set but not the list's own. Free the str's
// arena while the element lives, refill its memory with 'X' from a second
// arena, and count the X bytes the element reads. Above 0: a dead read.
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
    var l := List[str].New(&la)
    l.Push("") else {
        sa.Free()
        la.Free()
        return -3
    }
    l[0] = s
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

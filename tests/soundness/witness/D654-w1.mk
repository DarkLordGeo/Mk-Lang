// verdict: reject E0505
// note: a store through an untagged loan of *h reaches a Cell behind a shared field, so the caller's walk must count that cell. It must stay E0505: the narrowing "past a shared step only a view restores writing" (mutant M-D654-narrow in tests/soundness/mutants.txt) accepts it, and when run the caller's cell holds a reference to a dead local
struct H {
    c: &Cell[&i32]
    n: int
}

Pick(h: &H): &Cell[&i32] from h {
    return h.c
}

Sneak(h: mut H, x: &i32 into h) {
    c := Pick(&*h)
    c.Store(x)
}

Fill(h: &Heap, outer: &Cell[&i32]) {
    var hh := H(c: outer, n: 0)
    short: i32 = 2
    Sneak(mut hh, &short) // <- E0505
}

export Main(sys: &System) {
    long: i32 = 1
    oc := Cell[&i32].New(&long)
    Fill(sys.heap, &oc)
    v := oc.Load()
    if *v == 2 {
        return
    }
}
